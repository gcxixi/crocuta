package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"gitea.home.arpa/sundust/sentryx/internal/sentryx"
)

func main() {
	addr := flag.String("addr", envOr("SENTRYX_SERVER_ADDR", ":8080"), "listen address")
	role := flag.String("role", envOr("SENTRYX_SERVER_ROLE", "all"), "runtime role: api, worker, or all")
	flag.Parse()
	if *role != "api" && *role != "worker" && *role != "all" {
		slog.Error("invalid server role", "role", *role)
		os.Exit(2)
	}
	blobStore, err := sentryx.NewBlobStoreFromEnv()
	if err != nil {
		slog.Error("blob store configuration invalid", "error", err)
		os.Exit(2)
	}
	var store sentryx.EventStore
	var closeStore func() error
	if dsn := os.Getenv("SENTRYX_DATABASE_URL"); dsn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		postgres, err := sentryx.NewPostgresStore(ctx, dsn)
		cancel()
		if err != nil {
			slog.Error("postgres store unavailable", "error", err)
			os.Exit(1)
		}
		store = postgres
		postgres.SetBlobStore(blobStore)
		closeStore = postgres.Close
		if *role == "api" || *role == "all" {
			postgres.Async = true
		}
	}
	if *role == "worker" {
		postgres, ok := store.(*sentryx.PostgresStore)
		if !ok {
			slog.Error("worker role requires SENTRYX_DATABASE_URL")
			os.Exit(2)
		}
		defer postgres.Close()
		slog.Info("sentryx worker running")
		if err := postgres.RunWorker(context.Background(), 20, 250*time.Millisecond); err != nil {
			slog.Error("worker stopped", "error", err)
			os.Exit(1)
		}
		return
	}
	app := sentryx.NewApp(store)
	if blobStore != nil && store == nil {
		app.Artifacts.SetBlobStore(blobStore)
	}
	app.RelayToken = os.Getenv("SENTRYX_RELAY_TOKEN")
	app.ArtifactToken = os.Getenv("SENTRYX_ARTIFACT_TOKEN")
	app.ProjectKeys = sentryx.ParseProjectKeys(os.Getenv("SENTRYX_PROJECT_KEYS"))
	if value := os.Getenv("SENTRYX_RATE_LIMIT_PER_MINUTE"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit <= 0 {
			slog.Error("invalid SENTRYX_RATE_LIMIT_PER_MINUTE", "value", value)
			os.Exit(2)
		}
		app.RateLimiter = sentryx.NewRateLimiter(limit)
	}
	if closeStore != nil {
		defer closeStore()
	}
	if postgres, ok := store.(*sentryx.PostgresStore); ok && *role == "all" {
		go func() {
			if err := postgres.RunWorker(context.Background(), 20, 250*time.Millisecond); err != nil {
				slog.Error("worker stopped", "error", err)
			}
		}()
	}
	slog.Info("sentryx server listening", "addr", *addr)
	if err := http.ListenAndServe(*addr, app.Handler()); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
