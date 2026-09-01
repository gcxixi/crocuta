package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"gitea.home.arpa/sundust/sentryx/internal/sentryx"
)

func main() {
	addr := flag.String("addr", envOr("SENTRYX_SERVER_ADDR", ":8080"), "listen address")
	flag.Parse()
	app := sentryx.NewApp(nil)
	app.RelayToken = os.Getenv("SENTRYX_RELAY_TOKEN")
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
