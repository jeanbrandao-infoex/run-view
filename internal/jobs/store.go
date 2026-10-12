package jobs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// Store is shared by one API and one worker on the same Linux host/filesystem.
// The flock is inter-process, not an in-memory mutex. NFS is not supported.
type Store struct{ Root string }

func NewStore(root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return nil, err
	}
	return &Store{Root: absolute}, nil
}
func (store *Store) lock() (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(store.Root, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
func unlock(file *os.File)                 { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }
func (store *Store) path(id string) string { return filepath.Join(store.Root, id, "job.json") }
func (store *Store) read(id, tenant string) (Job, error) {
	if !ValidID(id) || !ValidID(tenant) {
		return Job{}, ErrNotFound
	}
	b, err := os.ReadFile(store.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	var job Job
	if err = json.Unmarshal(b, &job); err != nil {
		return Job{}, err
	}
	if job.TenantID != tenant || job.ID != id || !ValidID(job.ID) {
		return Job{}, ErrNotFound
	}
	return job, nil
}
func (store *Store) write(job Job) error {
	dir := filepath.Join(store.Root, job.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".job-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(b); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, store.path(job.ID)); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func (store *Store) Get(id, tenant string) (Job, error) {
	lock, err := store.lock()
	if err != nil {
		return Job{}, err
	}
	defer unlock(lock)
	return store.read(id, tenant)
}
func (store *Store) Create(tenant, key string, request Request) (Job, bool, error) {
	if !ValidID(tenant) || !ValidID(key) {
		return Job{}, false, errors.New("invalid tenant or idempotency key")
	}
	if err := request.Validate(); err != nil {
		return Job{}, false, err
	}
	body, _ := json.Marshal(request)
	hash := Hash(body)
	id := Hash([]byte(tenant + ":" + key))
	lock, err := store.lock()
	if err != nil {
		return Job{}, false, err
	}
	defer unlock(lock)
	existing, err := store.read(id, tenant)
	if err == nil {
		if existing.RequestHash != hash {
			return Job{}, false, ErrConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Job{}, false, err
	}
	entries, err := os.ReadDir(store.Root)
	if err != nil {
		return Job{}, false, err
	}
	// Hard bound on retained jobs, including terminal jobs: explicit deletion releases space.
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && ValidID(entry.Name()) {
			count++
		}
	}
	if count >= 100 {
		return Job{}, false, ErrCapacity
	}
	now := time.Now().UTC()
	job := Job{ID: id, TenantID: tenant, RequestHash: hash, State: "queued", CreatedAt: now, UpdatedAt: now, Request: request, Outputs: []Output{}}
	err = store.write(job)
	return job, true, err
}
func (store *Store) Update(id, tenant string, update func(*Job) error) (Job, error) {
	lock, err := store.lock()
	if err != nil {
		return Job{}, err
	}
	defer unlock(lock)
	job, err := store.read(id, tenant)
	if err != nil {
		return Job{}, err
	}
	if err = update(&job); err != nil {
		return Job{}, err
	}
	job.UpdatedAt = time.Now().UTC()
	return job, store.write(job)
}
func (store *Store) Cancel(id, tenant string) (Job, error) {
	return store.Update(id, tenant, func(job *Job) error {
		if Terminal(job.State) {
			return ErrConflict
		}
		if job.State == "queued" {
			job.Error = "cancel_cleanup_complete"
		}
		job.State = "cancelled"
		return nil
	})
}
func (store *Store) Claim() (Job, error) {
	lock, err := store.lock()
	if err != nil {
		return Job{}, err
	}
	defer unlock(lock)
	entries, err := os.ReadDir(store.Root)
	if err != nil {
		return Job{}, err
	}
	pending := []Job{}
	for _, entry := range entries {
		if !entry.IsDir() || !ValidID(entry.Name()) {
			continue
		}
		b, err := os.ReadFile(store.path(entry.Name()))
		if err != nil {
			return Job{}, err
		}
		var job Job
		if err = json.Unmarshal(b, &job); err != nil {
			return Job{}, err
		}
		if job.ID != entry.Name() || !ValidID(job.ID) || !ValidID(job.TenantID) {
			return Job{}, errors.New("invalid job identity")
		}
		if job.State == "queued" {
			pending = append(pending, job)
		}
	}
	if len(pending) == 0 {
		return Job{}, ErrNotFound
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.Before(pending[j].CreatedAt) })
	job := pending[0]
	job.State = "running"
	job.UpdatedAt = time.Now().UTC()
	return job, store.write(job)
}
func (store *Store) Recover() error {
	lock, err := store.lock()
	if err != nil {
		return err
	}
	defer unlock(lock)
	entries, err := os.ReadDir(store.Root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !ValidID(entry.Name()) {
			continue
		}
		b, err := os.ReadFile(store.path(entry.Name()))
		if err != nil {
			return err
		}
		var job Job
		if err = json.Unmarshal(b, &job); err != nil {
			return err
		}
		if job.ID != entry.Name() || !ValidID(job.ID) || !ValidID(job.TenantID) {
			return errors.New("invalid job identity")
		}
		if job.State == "running" || job.Error == "cleanup_failed" || job.State == "cancelled" && job.Error != "cancel_cleanup_complete" {
			if job.State == "running" {
				job.State = "failed"
				job.Error = "worker_interrupted"
			} else if job.Error == "cleanup_failed" {
				job.State = "failed"
				job.Error = "cleanup_recovered"
			} else {
				job.Error = "cancel_cleanup_complete"
			}
			job.UpdatedAt = time.Now().UTC()
			if err = store.write(job); err != nil {
				return err
			}
		}
	}
	return nil
}
func (store *Store) Delete(id, tenant string) error {
	lock, err := store.lock()
	if err != nil {
		return err
	}
	defer unlock(lock)
	job, err := store.read(id, tenant)
	if err != nil {
		return err
	}
	if !Terminal(job.State) || job.Error == "cleanup_failed" || job.State == "cancelled" && job.Error != "cancel_cleanup_complete" {
		return ErrConflict
	}
	return os.RemoveAll(filepath.Join(store.Root, id))
}
