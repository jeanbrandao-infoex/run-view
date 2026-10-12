package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"infoex.local/run/internal/jobs"
	"infoex.local/run/internal/runner"
)

type Worker struct {
	Store  *jobs.Store
	Runner runner.Runner
}

func (worker *Worker) Once(parent context.Context) error {
	job, err := worker.Store.Claim()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(job.Request.TimeoutSeconds)*time.Second)
	defer cancel()
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := worker.Store.Get(job.ID, job.TenantID)
				if err != nil || current.State == "cancelled" {
					cancel()
					return
				}
			}
		}
	}()
	result, runErr := worker.Runner.Run(ctx, job)
	if !errors.Is(runErr, runner.ErrCleanup) && ctx.Err() != nil {
		runErr = ctx.Err()
	}
	cancel()
	<-watcherDone
	_, err = worker.Store.Update(job.ID, job.TenantID, func(current *jobs.Job) error {
		if errors.Is(runErr, runner.ErrCleanup) {
			current.State = "failed"
			current.Error = "cleanup_failed"
			return nil
		}
		if current.State == "cancelled" {
			current.Error = "cancel_cleanup_complete"
			return nil
		}
		if runErr != nil {
			current.State = "failed"
			current.Error = "execution_failed"
			if errors.Is(runErr, context.DeadlineExceeded) {
				current.Error = "execution_timeout"
			}
			return nil
		}
		current.State = "completed"
		current.Outputs = result.Outputs
		current.Summary = result.Summary
		return nil
	})
	return err
}
func (worker *Worker) Loop(ctx context.Context) error {
	// Hold a separate lock for the worker lifetime: no concurrent worker/recovery.
	file, err := os.OpenFile(filepath.Join(worker.Store.Root, ".worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("one worker only")
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	if docker, ok := worker.Runner.(*runner.Docker); ok {
		startup, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = docker.Ready(startup)
		if err == nil {
			err = docker.Cleanup(startup)
		}
		cancel()
		if err != nil {
			return err
		}
	}
	if err = worker.Store.Recover(); err != nil {
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err = worker.Once(ctx); err != nil && !errors.Is(err, jobs.ErrNotFound) {
				return err
			}
		}
	}
}
