package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	piiConfig := sentryx.PIIConfigFromEnv()
	blobStore, err := sentryx.NewBlobStoreFromEnv()
	if err != nil {
		slog.Error("blob store configuration invalid", "error", err)
		os.Exit(2)
	}
	var store sentryx.EventStore
	var closeStore func() error
	if dsn := os.Getenv("SENTRYX_DATABASE_URL"); dsn != "" {
		dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		postgres, err := sentryx.NewPostgresStore(dbCtx, dsn)
		cancel()
		if err != nil {
			slog.Error("postgres store unavailable", "error", err)
			os.Exit(1)
		}
		store = postgres
		postgres.SetPIIConfig(piiConfig)
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
		if err := postgres.RunWorker(ctx, 20, 250*time.Millisecond); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("worker stopped", "error", err)
			os.Exit(1)
		}
		slog.Info("sentryx worker stopped cleanly")
		return
	}
	app := sentryx.NewApp(store)
	app.SetPIIConfig(piiConfig)
	if blobStore != nil && store == nil {
		app.Artifacts.SetBlobStore(blobStore)
	}
	app.RelayToken = os.Getenv("SENTRYX_RELAY_TOKEN")
	app.ArtifactToken = os.Getenv("SENTRYX_ARTIFACT_TOKEN")
	app.APITokens = sentryx.ParseAPITokens(os.Getenv("SENTRYX_API_TOKENS"))
	app.CurrentUserID = envOr("SENTRYX_CURRENT_USER_ID", "1")
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
	var workerWg sync.WaitGroup
	if postgres, ok := store.(*sentryx.PostgresStore); ok && *role == "all" {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			if err := postgres.RunWorker(ctx, 20, 250*time.Millisecond); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("worker stopped", "error", err)
			}
		}()
	}
	server := &http.Server{
		Addr:    *addr,
		Handler: app.Handler(),
	}
	go func() {
		<-ctx.Done()
		slog.Info("shutting down sentryx server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("sentryx server listening", "addr", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
	workerWg.Wait()
	slog.Info("sentryx server stopped cleanly")
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
