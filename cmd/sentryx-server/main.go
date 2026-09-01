package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"gitea.home.arpa/sundust/sentryx/internal/sentryx"
)

func main() {
	addr := flag.String("addr", envOr("SENTRYX_SERVER_ADDR", ":8080"), "listen address")
	flag.Parse()
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
		closeStore = postgres.Close
	}
	app := sentryx.NewApp(store)
	app.RelayToken = os.Getenv("SENTRYX_RELAY_TOKEN")
	if closeStore != nil {
		defer closeStore()
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
