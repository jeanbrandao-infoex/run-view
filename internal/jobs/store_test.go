package jobs

import (
	"errors"
	"sync"
	"testing"
)

func TestConcurrentCreateAndPersistentClaim(t *testing.T) {
	root := t.TempDir()
	store, _ := NewStore(root)
	var wait sync.WaitGroup
	ids := make(chan string, 20)
	for range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			job, _, err := store.Create("tenant", "key", Request{Code: "print(1)"})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- job.ID
		}()
	}
	wait.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate job")
		}
	}
	// A separate Store sees the same queue and prevents a second claim.
	second, _ := NewStore(root)
	job, err := second.Claim()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = second.Recover(); err != nil {
		t.Fatal(err)
	}
	job, err = store.Get(job.ID, "tenant")
	if err != nil || job.State != "failed" || job.Error != "worker_interrupted" {
		t.Fatal(job.State, err)
	}
}
func TestInputValidation(t *testing.T) {
	for _, request := range []Request{{Code: "x", Inputs: []Input{{Name: "../x", Data: ""}}}, {Code: "x", TimeoutSeconds: 301}, {Code: "x", Inputs: []Input{{Name: "x.csv", Data: "notbase64"}}}, {Code: "x", Inputs: []Input{{Name: "main.py"}}}} {
		if request.Validate() == nil {
			t.Fatal("unsafe input accepted")
		}
	}
}
