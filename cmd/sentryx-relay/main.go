package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"gitea.home.arpa/sundust/sentryx/internal/sentryx"
)

func main() {
	addr := flag.String("addr", envOr("SENTRYX_RELAY_ADDR", ":8081"), "listen address")
	upstream := flag.String("upstream", envOr("SENTRYX_SERVER_URL", "http://127.0.0.1:8080"), "server upstream")
	flag.Parse()
	mirror := os.Getenv("SENTRYX_MIRROR_URL")
	slog.Info("sentryx relay listening", "addr", *addr, "upstream", *upstream, "mirror", mirror != "")
	if err := http.ListenAndServe(*addr, sentryx.NewRelayWithConfigAndMirror(*upstream, mirror, sentryx.DefaultMaxEnvelopeBytes, os.Getenv("SENTRYX_RELAY_TOKEN"), os.Getenv("SENTRYX_MIRROR_RELAY_TOKEN"), sentryx.PIIConfigFromEnv())); err != nil {
		slog.Error("relay stopped", "error", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
