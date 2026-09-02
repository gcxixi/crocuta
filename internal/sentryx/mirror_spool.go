package sentryx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type mirrorSpoolRecord struct {
	URL    string              `json:"url"`
	Method string              `json:"method"`
	Header map[string][]string `json:"header"`
	Body   string              `json:"body"`
}

type MirrorSpool struct {
	dir    string
	client *http.Client
	token  string
}

func NewMirrorSpool(dir string, tokens ...string) *MirrorSpool {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil
	}
	var token string
	if len(tokens) > 0 {
		token = tokens[0]
	}
	return &MirrorSpool{dir: dir, client: &http.Client{Timeout: 10 * time.Second}, token: token}
}

func (s *MirrorSpool) Save(request *http.Request, body []byte) error {
	if s == nil || s.dir == "" {
		return nil
	}
	digest := sha256.Sum256(append([]byte(request.URL.String()), body...))
	headers := make(map[string][]string, len(request.Header))
	for key, values := range request.Header {
		switch strings.ToLower(key) {
		case "authorization", "cookie", "x-sentry-auth", "x-sentryx-relay-token", "x-sentryx-management-token":
			continue
		default:
			headers[key] = append([]string(nil), values...)
		}
	}
	record := mirrorSpoolRecord{URL: request.URL.String(), Method: request.Method, Header: headers, Body: base64.StdEncoding.EncodeToString(body)}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "."+hex.EncodeToString(digest[:])+".tmp")
	name := filepath.Join(s.dir, hex.EncodeToString(digest[:])+".json")
	if err := os.WriteFile(tmp, encoded, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

func (s *MirrorSpool) Run(ctx context.Context) {
	if s == nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.replay(ctx)
		}
	}
}

func (s *MirrorSpool) replay(ctx context.Context) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		filename := filepath.Join(s.dir, entry.Name())
		encoded, err := os.ReadFile(filename)
		if err != nil {
			continue
		}
		var record mirrorSpoolRecord
		if json.Unmarshal(encoded, &record) != nil {
			continue
		}
		body, err := base64.StdEncoding.DecodeString(record.Body)
		if err != nil {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, record.Method, record.URL, bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header = http.Header(record.Header)
		if s.token != "" {
			req.Header.Set("X-SentryX-Relay-Token", s.token)
		}
		response, err := s.client.Do(req)
		if err != nil {
			continue
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			_ = os.Remove(filename)
		}
	}
}
