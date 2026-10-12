package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"infoex.local/run/internal/api"
	"infoex.local/run/internal/jobs"
)

func main() {
	token := os.Getenv("INFOEX_RUN_TOKEN")
	if len(token) < 32 {
		log.Fatal("INFOEX_RUN_TOKEN must contain at least 32 characters")
	}
	root := os.Getenv("INFOEX_RUN_DATA")
	if root == "" {
		root = "./data"
	}
	store, err := jobs.NewStore(root)
	if err != nil {
		log.Fatal("cannot open store")
	}
	address := os.Getenv("INFOEX_RUN_ADDR")
	if address == "" {
		address = "127.0.0.1:8090"
	}
	service := &api.API{Store: store, Token: token}
	server := &http.Server{Addr: address, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Printf("Infoex Run API listening on %s", address)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("server failed")
	}
}
