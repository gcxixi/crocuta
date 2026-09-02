package sentryx

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
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
	return NewRelayWithConfigAndMirrorAndPolicy(upstream, "", maxBody, relayToken, "", piiConfig, ItemPolicyFromEnv())
}

// NewRelayWithConfigAndMirror forwards the same Sentry-compatible request to
// the primary upstream and, when configured, mirrors ingestion requests to a
// second upstream. The primary response is never blocked by mirror failure.
func NewRelayWithConfigAndMirror(upstream, mirror string, maxBody int64, relayToken, mirrorToken string, piiConfig PIIConfig) http.Handler {
	return NewRelayWithConfigAndMirrorAndPolicy(upstream, mirror, maxBody, relayToken, mirrorToken, piiConfig, ItemPolicyFromEnv())
}

func NewRelayWithConfigAndMirrorAndPolicy(upstream, mirror string, maxBody int64, relayToken, mirrorToken string, piiConfig PIIConfig, itemPolicy ItemPolicy) http.Handler {
	if maxBody <= 0 {
		maxBody = DefaultMaxEnvelopeBytes
	}
	client := &http.Client{Timeout: 10 * time.Second}
	mirrorSlots := make(chan struct{}, 32)
	spool := NewMirrorSpool(os.Getenv("SENTRYX_MIRROR_SPOOL_DIR"), mirrorToken)
	if spool != nil {
		go spool.Run(context.Background())
	}
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
		if r.URL.Path == "/health/ready" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
		if r.URL.Path == "/metrics" {
			DefaultMetrics.ServeHTTP(w, r)
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
		if isEnvelopePath(r.URL.Path) {
			body, _, err = ApplyItemPolicy(body, itemPolicy)
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
			mirrorContext, cancelMirror := context.WithTimeout(context.Background(), 10*time.Second)
			mirrorReq, mirrorErr := newRelayRequestWithContext(mirrorContext, r, strings.TrimRight(mirror, "/")+r.URL.RequestURI(), body, scrub, mirrorToken)
			if mirrorErr == nil {
				select {
				case mirrorSlots <- struct{}{}:
					go func() {
						defer cancelMirror()
						defer func() { <-mirrorSlots }()
						mirrorRelayRequest(client, mirrorReq, spool)
					}()
				default:
					cancelMirror()
					slog.Default().Warn("relay mirror capacity exhausted", "path", r.URL.Path)
				}
			} else {
				cancelMirror()
				slog.Default().Warn("relay mirror request build failed", "error", mirrorErr, "path", r.URL.Path)
			}
		}
		response, err := client.Do(req)
		if err != nil {
			DefaultMetrics.Inc("sentryx_relay_requests_total", map[string]string{"status": "error"})
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		copyRelayResponseHeaders(w.Header(), response.Header)
		DefaultMetrics.Inc("sentryx_relay_requests_total", map[string]string{"status": strconv.Itoa(response.StatusCode)})
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	})
}

func newRelayRequest(source *http.Request, targetURL string, body []byte, scrub bool, token string) (*http.Request, error) {
	return newRelayRequestWithContext(source.Context(), source, targetURL, body, scrub, token)
}

func newRelayRequestWithContext(ctx context.Context, source *http.Request, targetURL string, body []byte, scrub bool, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, source.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range source.Header {
		if isHopByHopHeader(key) {
			continue
		}
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

func copyRelayResponseHeaders(destination, source http.Header) {
	for key, values := range source {
		if isHopByHopHeader(key) || strings.HasPrefix(strings.ToLower(key), "access-control-") {
			continue
		}
		destination.Del(key)
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

func isHopByHopHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func mirrorRelayRequest(client *http.Client, request *http.Request, spools ...*MirrorSpool) {
	response, err := client.Do(request)
	if err != nil {
		if len(spools) > 0 && spools[0] != nil && request.GetBody != nil {
			if body, bodyErr := request.GetBody(); bodyErr == nil {
				data, _ := io.ReadAll(body)
				body.Close()
				_ = spools[0].Save(request, data)
				DefaultMetrics.Inc("sentryx_mirror_spooled_total", nil)
			}
		}
		DefaultMetrics.Inc("sentryx_mirror_requests_total", map[string]string{"status": "error"})
		slog.Default().Warn("relay mirror failed", "error", err, "url", request.URL.Path)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= http.StatusBadRequest {
		if len(spools) > 0 && spools[0] != nil && request.GetBody != nil {
			if body, bodyErr := request.GetBody(); bodyErr == nil {
				data, _ := io.ReadAll(body)
				body.Close()
				_ = spools[0].Save(request, data)
				DefaultMetrics.Inc("sentryx_mirror_spooled_total", nil)
			}
		}
		DefaultMetrics.Inc("sentryx_mirror_requests_total", map[string]string{"status": "rejected"})
		slog.Default().Warn("relay mirror rejected", "status", response.StatusCode, "url", request.URL.Path)
		return
	}
	DefaultMetrics.Inc("sentryx_mirror_requests_total", map[string]string{"status": "accepted"})
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
