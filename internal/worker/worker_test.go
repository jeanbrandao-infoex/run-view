package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"infoex.local/run/internal/jobs"
	"infoex.local/run/internal/runner"
)

type fakeRunner struct{ Fail bool }

func (fake fakeRunner) Run(ctx context.Context, job jobs.Job) (runner.Result, error) {
	if fake.Fail {
		return runner.Result{}, errors.New("provider secret should not leak")
	}
	return runner.Result{Summary: "3 accounts processed", Outputs: []jobs.Output{{Name: "result.csv", Size: 10}}}, nil
}
func TestWorkerPersistsTerminalResult(t *testing.T) {
	store, _ := jobs.NewStore(t.TempDir())
	job, _, _ := store.Create("tenant", "key", jobs.Request{Code: "print(1)"})
	service := &Worker{Store: store, Runner: fakeRunner{}}
	if err := service.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(job.ID, "tenant")
	if got.State != "completed" || len(got.Outputs) != 1 {
		t.Fatal(got)
	}
	job, _, _ = store.Create("tenant", "key2", jobs.Request{Code: "print(1)"})
	service.Runner = fakeRunner{Fail: true}
	if err := service.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get(job.ID, "tenant")
	if got.State != "failed" || strings.Contains(got.Error, "secret") {
		t.Fatal(got)
	}
}
