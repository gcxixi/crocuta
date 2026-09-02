package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gcxixi/crocuta/internal/sentryx"
)

func main() {
	addr := flag.String("addr", envOr("SENTRYX_RELAY_ADDR", ":8081"), "listen address")
	upstream := flag.String("upstream", envOr("SENTRYX_SERVER_URL", "http://127.0.0.1:8080"), "server upstream")
	flag.Parse()
	mirror := os.Getenv("SENTRYX_MIRROR_URL")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	handler := sentryx.NewRelayWithConfigAndMirror(*upstream, mirror, sentryx.DefaultMaxEnvelopeBytes, os.Getenv("SENTRYX_RELAY_TOKEN"), os.Getenv("SENTRYX_MIRROR_RELAY_TOKEN"), sentryx.PIIConfigFromEnv())
	server := &http.Server{
		Addr:    *addr,
		Handler: handler,
	}
	go func() {
		<-ctx.Done()
		slog.Info("shutting down sentryx relay...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("sentryx relay listening", "addr", *addr, "upstream", *upstream, "mirror", mirror != "")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("relay stopped", "error", err)
		os.Exit(1)
	}
	slog.Info("sentryx relay stopped cleanly")
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
