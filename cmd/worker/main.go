package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"context"
	"infoex.local/run/internal/jobs"
	"infoex.local/run/internal/runner"
	"infoex.local/run/internal/worker"
)

func main() {
	root := os.Getenv("INFOEX_RUN_DATA")
	if root == "" {
		root = "./data"
	}
	store, err := jobs.NewStore(root)
	if err != nil {
		log.Fatal("cannot open store")
	}
	executor := &runner.Docker{Root: store.Root, Image: os.Getenv("INFOEX_RUN_IMAGE")}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	service := &worker.Worker{Store: store, Runner: executor}
	if err = service.Loop(ctx); err != nil {
		log.Fatal(err)
	}
}
