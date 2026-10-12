package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"infoex.local/run/internal/jobs"
)

type API struct {
	Store *jobs.Store
	Token string
}

func (api *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(res http.ResponseWriter, req *http.Request) {
		respond(res, 200, map[string]string{"status": "alive"})
	})
	uploads := make(chan struct{}, 1)
	mux.HandleFunc("POST /v1/jobs", func(res http.ResponseWriter, req *http.Request) {
		select {
		case uploads <- struct{}{}:
			defer func() { <-uploads }()
			api.create(res, req)
		default:
			respond(res, 429, map[string]string{"error": "upload_busy"})
		}
	})
	mux.HandleFunc("GET /v1/jobs/{id}", api.get)
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", api.cancel)
	mux.HandleFunc("GET /v1/jobs/{id}/outputs", api.outputs)
	mux.HandleFunc("GET /v1/jobs/{id}/outputs/{name}", api.download)
	mux.HandleFunc("DELETE /v1/jobs/{id}", api.delete)
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.Header().Set("Cache-Control", "no-store")
		res.Header().Set("X-Content-Type-Options", "nosniff")
		if req.URL.Path != "/healthz" {
			token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
			if len(api.Token) < 32 || !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(token), []byte(api.Token)) != 1 {
				respond(res, 401, map[string]string{"error": "unauthorized"})
				return
			}
			if !jobs.ValidID(req.Header.Get("X-Tenant-ID")) {
				respond(res, 400, map[string]string{"error": "invalid_tenant"})
				return
			}
		}
		mux.ServeHTTP(res, req)
	})
}
func respond(res http.ResponseWriter, status int, value any) {
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(status)
	json.NewEncoder(res).Encode(value)
}
func failure(res http.ResponseWriter, err error) {
	status := 500
	message := "internal_error"
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		status = 404
		message = "not_found"
	case errors.Is(err, jobs.ErrConflict):
		status = 409
		message = "conflict"
	case errors.Is(err, jobs.ErrCapacity):
		status = 429
		message = "capacity_exceeded"
	}
	respond(res, status, map[string]string{"error": message})
}
func view(job jobs.Job) any {
	return struct {
		ID        string        `json:"id"`
		State     string        `json:"state"`
		CreatedAt time.Time     `json:"created_at"`
		UpdatedAt time.Time     `json:"updated_at"`
		Outputs   []jobs.Output `json:"outputs"`
		Summary   string        `json:"summary,omitempty"`
		Error     string        `json:"error,omitempty"`
	}{job.ID, job.State, job.CreatedAt, job.UpdatedAt, job.Outputs, job.Summary, job.Error}
}
func (api *API) create(res http.ResponseWriter, req *http.Request) {
	key := req.Header.Get("Idempotency-Key")
	if !jobs.ValidID(key) {
		respond(res, 400, map[string]string{"error": "invalid_idempotency_key"})
		return
	}
	req.Body = http.MaxBytesReader(res, req.Body, 34<<20)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	var request jobs.Request
	if err := decoder.Decode(&request); err != nil {
		respond(res, 400, map[string]string{"error": "invalid_body"})
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		respond(res, 400, map[string]string{"error": "invalid_body"})
		return
	}
	if err := request.Validate(); err != nil {
		respond(res, 400, map[string]string{"error": err.Error()})
		return
	}
	job, created, err := api.Store.Create(req.Header.Get("X-Tenant-ID"), key, request)
	if err != nil {
		failure(res, err)
		return
	}
	status := 200
	if created {
		status = 202
	}
	respond(res, status, view(job))
}
func (api *API) get(res http.ResponseWriter, req *http.Request) {
	job, err := api.Store.Get(req.PathValue("id"), req.Header.Get("X-Tenant-ID"))
	if err != nil {
		failure(res, err)
		return
	}
	respond(res, 200, view(job))
}
func (api *API) cancel(res http.ResponseWriter, req *http.Request) {
	job, err := api.Store.Cancel(req.PathValue("id"), req.Header.Get("X-Tenant-ID"))
	if err != nil {
		failure(res, err)
		return
	}
	respond(res, 200, view(job))
}
func (api *API) outputs(res http.ResponseWriter, req *http.Request) {
	job, err := api.Store.Get(req.PathValue("id"), req.Header.Get("X-Tenant-ID"))
	if err != nil {
		failure(res, err)
		return
	}
	if job.State != "completed" {
		failure(res, jobs.ErrConflict)
		return
	}
	respond(res, 200, job.Outputs)
}
func (api *API) download(res http.ResponseWriter, req *http.Request) {
	job, err := api.Store.Get(req.PathValue("id"), req.Header.Get("X-Tenant-ID"))
	if err != nil {
		failure(res, err)
		return
	}
	name := req.PathValue("name")
	if job.State != "completed" || !jobs.ValidOutput(name) {
		failure(res, jobs.ErrNotFound)
		return
	}
	found := false
	for _, output := range job.Outputs {
		if output.Name == name {
			found = true
		}
	}
	if !found {
		failure(res, jobs.ErrNotFound)
		return
	}
	file, err := os.Open(filepath.Join(api.Store.Root, job.ID, "output", name))
	if err != nil {
		failure(res, jobs.ErrNotFound)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > jobs.MaxOutputBytes {
		failure(res, jobs.ErrNotFound)
		return
	}
	res.Header().Set("Content-Type", "application/octet-stream")
	res.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	res.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	io.Copy(res, file)
}
func (api *API) delete(res http.ResponseWriter, req *http.Request) {
	if err := api.Store.Delete(req.PathValue("id"), req.Header.Get("X-Tenant-ID")); err != nil {
		failure(res, err)
		return
	}
	res.WriteHeader(204)
}
