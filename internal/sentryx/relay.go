package sentryx

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// NewRelay creates the stateless edge handler with the privacy-first default
// policy. NewRelayWithConfig is available when operators need to disable or
// customize edge scrubbing.
func NewRelay(upstream string, maxBody int64, relayToken string) http.Handler {
	return NewRelayWithConfig(upstream, maxBody, relayToken, DefaultPIIConfig())
}

// NewRelayWithConfig creates the stateless edge handler. When PII is enabled,
// gzip bodies are decompressed, scrubbed, and forwarded uncompressed so the
// server never needs to persist an unsanitized Envelope.
func NewRelayWithConfig(upstream string, maxBody int64, relayToken string, piiConfig PIIConfig) http.Handler {
	return NewRelayWithConfigAndMirror(upstream, "", maxBody, relayToken, "", piiConfig)
}

// NewRelayWithConfigAndMirror forwards the same Sentry-compatible request to
// the primary upstream and, when configured, mirrors ingestion requests to a
// second upstream. The primary response is never blocked by mirror failure.
func NewRelayWithConfigAndMirror(upstream, mirror string, maxBody int64, relayToken, mirrorToken string, piiConfig PIIConfig) http.Handler {
	if maxBody <= 0 {
		maxBody = DefaultMaxEnvelopeBytes
	}
	client := &http.Client{Timeout: 10 * time.Second}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			setCORSHeaders(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/health/live" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		setCORSHeaders(w)
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		scrub := piiConfig.Enabled && isEnvelopePath(r.URL.Path)
		body, compressed, err := readRelayBody(w, r, maxBody, scrub)
		if err != nil {
			http.Error(w, "envelope too large", http.StatusRequestEntityTooLarge)
			return
		}
		if scrub {
			body, err = ScrubEnvelope(body, piiConfig)
			if err != nil {
				http.Error(w, "invalid envelope", http.StatusBadRequest)
				return
			}
		}
		req, err := newRelayRequest(r, strings.TrimRight(upstream, "/")+r.URL.RequestURI(), body, scrub, relayToken)
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		if compressed && !scrub {
			req.Header.Set("Content-Encoding", "gzip")
		}
		if mirror != "" && isEnvelopePath(r.URL.Path) {
			mirrorReq, mirrorErr := newRelayRequest(r, strings.TrimRight(mirror, "/")+r.URL.RequestURI(), body, scrub, mirrorToken)
			if mirrorErr == nil {
				go mirrorRelayRequest(client, mirrorReq)
			} else {
				slog.Default().Warn("relay mirror request build failed", "error", mirrorErr, "path", r.URL.Path)
			}
		}
		response, err := client.Do(req)
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	})
}

func newRelayRequest(source *http.Request, targetURL string, body []byte, scrub bool, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(source.Context(), source.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range source.Header {
		if scrub && (strings.EqualFold(key, "Content-Encoding") || strings.EqualFold(key, "Content-Length")) {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if token != "" {
		req.Header.Set("X-SentryX-Relay-Token", token)
	}
	return req, nil
}

func mirrorRelayRequest(client *http.Client, request *http.Request) {
	response, err := client.Do(request)
	if err != nil {
		slog.Default().Warn("relay mirror failed", "error", err, "url", request.URL.Path)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= http.StatusBadRequest {
		slog.Default().Warn("relay mirror rejected", "status", response.StatusCode, "url", request.URL.Path)
	}
}

func isEnvelopePath(value string) bool {
	value = strings.TrimSuffix(value, "/")
	return strings.HasSuffix(value, "/envelope") || strings.HasSuffix(value, "/store")
}

func readRelayBody(w http.ResponseWriter, r *http.Request, maxBody int64, decompress bool) ([]byte, bool, error) {
	compressed := strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "gzip")
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return nil, compressed, err
	}
	if !compressed || !decompress {
		return raw, compressed, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, compressed, err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxBody+1))
	if err != nil || int64(len(body)) > maxBody {
		return nil, compressed, errors.New("decompressed envelope too large")
	}
	return body, compressed, nil
}
