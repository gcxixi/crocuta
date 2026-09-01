package sentryx

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"
)

// NewRelay creates the stateless edge handler used by the first vertical
// slice. It enforces a bounded body and forwards the original Sentry path and
// query to Server, keeping DSN compatibility at the edge.
func NewRelay(upstream string, maxBody int64, relayToken string) http.Handler {
	if maxBody <= 0 {
		maxBody = DefaultMaxEnvelopeBytes
	}
	client := &http.Client{Timeout: 10 * time.Second}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/live" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			http.Error(w, "envelope too large", http.StatusRequestEntityTooLarge)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, strings.TrimRight(upstream, "/")+r.URL.RequestURI(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		for key, values := range r.Header {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
		if relayToken != "" {
			req.Header.Set("X-SentryX-Relay-Token", relayToken)
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
