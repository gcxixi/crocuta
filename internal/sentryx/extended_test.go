package sentryx

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStoreIngestsContextClientReportAttachmentAndSignals(t *testing.T) {
	store := NewStore()
	event := map[string]any{
		"event_id":    "11111111111111111111111111111111",
		"platform":    "javascript",
		"message":     "checkout failed",
		"user":        map[string]any{"id": "u-1", "ip_address": "203.0.113.1"},
		"request":     map[string]any{"url": "https://example.test/checkout?token=secret", "method": "POST", "headers": map[string]any{"Authorization": "Bearer abc"}},
		"breadcrumbs": map[string]any{"values": []any{map[string]any{"category": "ui.click", "message": "pay"}}},
		"extra":       map[string]any{"password": "secret", "cart_size": 2},
	}
	eventBody, _ := json.Marshal(event)
	reportBody := []byte(`{"timestamp":"2026-09-01T00:00:00Z","discarded_events":[{"reason":"sample_rate","category":"error","quantity":2}]}`)
	attachmentBody := []byte("console output")
	signalBody := []byte(`{"transaction":"checkout","spans":[]}`)
	logBody := []byte(`{"body":"password=secret","attributes":{"token":"abc"}}`)
	body := testEnvelope(
		envelopePart(`{"type":"event","length":`+itoa(len(eventBody))+`}`, eventBody),
		envelopePart(`{"type":"client_report","length":`+itoa(len(reportBody))+`}`, reportBody),
		envelopePart(`{"type":"attachment","length":`+itoa(len(attachmentBody)+0)+`,"filename":"console.log","content_type":"text/plain","event_id":"11111111111111111111111111111111"}`, attachmentBody),
		envelopePart(`{"type":"transaction","length":`+itoa(len(signalBody))+`,"event_id":"22222222222222222222222222222222"}`, signalBody),
		envelopePart(`{"type":"log","length":`+itoa(len(logBody))+`}`, logBody),
	)
	accepted, err := store.Ingest("1", "public", body)
	if err != nil || accepted != 5 {
		t.Fatalf("accepted=%d err=%v", accepted, err)
	}
	events := store.ListEvents("1", "")
	if len(events) != 1 || events[0].User == nil || events[0].User.IPAddress != "[Filtered]" {
		t.Fatalf("events=%#v", events)
	}
	if events[0].Request == nil || events[0].Request.Headers["Authorization"] != "[Filtered]" {
		t.Fatalf("request=%#v", events[0].Request)
	}
	if len(store.ListClientReports("1")) != 1 || len(store.ListAttachments("1", "11111111111111111111111111111111")) != 1 || len(store.ListSignals("1", "transaction")) != 1 || len(store.ListSignals("1", "log")) != 1 {
		t.Fatalf("extended items missing")
	}
	if payload := store.ListSignals("1", "log")[0].Payload; strings.Contains(string(payload), "secret") || !strings.Contains(string(payload), "[Filtered]") {
		t.Fatalf("log was not scrubbed: %q", payload)
	}
}

func TestReleaseAndArtifactManagementAPI(t *testing.T) {
	app := NewApp(nil)
	app.ArtifactToken = "management"
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/0/projects/1/releases", bytes.NewBufferString(`{"version":"web@1.0.0"}`))
	request.Header.Set("X-SentryX-Management-Token", "management")
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("release create status=%v err=%v", response.StatusCode, err)
	}
	response.Body.Close()
	response, err = http.Get(server.URL + "/api/0/projects/1/releases")
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("release list status=%v err=%v", response.StatusCode, err)
	}
	response.Body.Close()
}

func TestGzipEnvelopeAndCORS(t *testing.T) {
	app := NewApp(nil)
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	eventBody, _ := json.Marshal(map[string]any{"event_id": "33333333333333333333333333333333", "message": "gzip"})
	envelope := testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(eventBody))+`}`, eventBody))
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(envelope)
	_ = writer.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/1/envelope?sentry_key=public", &compressed)
	request.Header.Set("Content-Encoding", "gzip")
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("gzip status=%v err=%v", response.StatusCode, err)
	}
	response.Body.Close()
	preflight, _ := http.NewRequest(http.MethodOptions, server.URL+"/api/1/envelope", nil)
	preflightResponse, err := http.DefaultClient.Do(preflight)
	if err != nil || preflightResponse.StatusCode != http.StatusNoContent || preflightResponse.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("cors status=%v err=%v", preflightResponse.StatusCode, err)
	}
	preflightResponse.Body.Close()
}

func TestScrubEnvelopeSupportsRulesTextAttachmentsAndSignals(t *testing.T) {
	event := []byte(`{"event_id":"44444444444444444444444444444444","message":"card 4111 1111 1111 1111","extra":{"password":"secret","keep":"ok"},"request":{"headers":{"Authorization":"Bearer secret"},"query_string":"token=abc"},"user":{"ip_address":"203.0.113.9"}}`)
	attachment := []byte("password=secret\ncard=4111 1111 1111 1111")
	signal := []byte(`{"message":"token=signal"}`)
	body := testEnvelope(
		envelopePart(`{"type":"event","length":`+itoa(len(event))+`}`, event),
		envelopePart(`{"type":"attachment","length":`+itoa(len(attachment))+`,"filename":"debug.log","content_type":"text/plain"}`, attachment),
		envelopePart(`{"type":"log","length":`+itoa(len(signal))+`}`, signal),
	)
	config := DefaultPIIConfig()
	config.Rules = []PIIRule{{ID: "keep-hash", Selector: "extra.keep", Action: "hash", Type: "anything"}}
	cleaned, err := ScrubEnvelope(body, config)
	if err != nil {
		t.Fatal(err)
	}
	items, err := parseEnvelope(cleaned)
	if err != nil || len(items) != 3 {
		t.Fatalf("parse cleaned envelope: %v items=%d", err, len(items))
	}
	var cleanEvent map[string]any
	if err := json.Unmarshal(items[0].Payload, &cleanEvent); err != nil {
		t.Fatal(err)
	}
	if cleanEvent["message"] != "[Filtered]" || cleanEvent["extra"].(map[string]any)["password"] != "[Filtered]" {
		t.Fatalf("event was not scrubbed: %#v", cleanEvent)
	}
	if cleanEvent["extra"].(map[string]any)["keep"] == "ok" {
		t.Fatal("custom hash rule was not applied")
	}
	if meta, ok := cleanEvent["_meta"].(map[string]any); ok {
		for key := range meta {
			if strings.HasPrefix(key, "_meta.") {
				t.Fatalf("scrub metadata was recursively scrubbed: %#v", meta)
			}
		}
	}
	if !strings.Contains(string(items[1].Payload), "[Filtered]") || !strings.Contains(string(items[2].Payload), "[Filtered]") {
		t.Fatalf("text items were not scrubbed: %q %q", items[1].Payload, items[2].Payload)
	}
}

func TestScrubEnvelopeCanBeDisabled(t *testing.T) {
	event := []byte(`{"message":"password=secret"}`)
	body := testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(event))+`}`, event))
	cleaned, err := ScrubEnvelope(body, PIIConfig{Enabled: false})
	if err != nil || string(cleaned) != string(body) {
		t.Fatalf("disabled scrub changed body: err=%v body=%q", err, cleaned)
	}
}

func TestSentryControlPlaneAPI(t *testing.T) {
	app := NewApp(nil)
	app.APITokens = map[string]string{"management": "1"}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	do := func(method, endpoint, body string) (*http.Response, []byte) {
		req, err := http.NewRequest(method, server.URL+endpoint, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer management")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, data
	}
	resp, body := do(http.MethodGet, "/api/0/organizations/", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"slug":"default"`) {
		t.Fatalf("organizations status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = do(http.MethodPost, "/api/0/organizations/default/teams/", `{"name":"Frontend"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("team status=%d body=%s", resp.StatusCode, body)
	}
	var team Team
	if err := json.Unmarshal(body, &team); err != nil || team.Slug != "frontend" {
		t.Fatalf("team=%s err=%v", body, err)
	}
	resp, body = do(http.MethodPost, "/api/0/organizations/default/projects/", `{"name":"Web App","platform":"javascript"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("project status=%d body=%s", resp.StatusCode, body)
	}
	var project ControlProject
	if err := json.Unmarshal(body, &project); err != nil || project.Slug != "web-app" || len(project.Keys) != 1 {
		t.Fatalf("project=%s err=%v", body, err)
	}
	resp, _ = do(http.MethodPost, "/api/0/projects/default/"+project.Slug+"/teams/"+team.ID, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("project team status=%d", resp.StatusCode)
	}
	resp, body = do(http.MethodGet, "/api/0/projects/default/"+project.Slug, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), team.ID) {
		t.Fatalf("project get status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = do(http.MethodGet, "/api/0/users/me", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"id":"1"`) {
		t.Fatalf("user status=%d body=%s", resp.StatusCode, body)
	}
}

func TestRelayScrubsBeforeForwarding(t *testing.T) {
	event := []byte(`{"event_id":"55555555555555555555555555555555","request":{"headers":{"Authorization":"Bearer relay-secret"}}}`)
	envelope := testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(event))+`}`, event))
	var forwarded []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted":1}`))
	}))
	defer upstream.Close()
	relay := httptest.NewServer(NewRelayWithConfig(upstream.URL, DefaultMaxEnvelopeBytes, "", DefaultPIIConfig()))
	defer relay.Close()
	request, _ := http.NewRequest(http.MethodPost, relay.URL+"/api/1/envelope?sentry_key=public", bytes.NewReader(envelope))
	request.Header.Set("Content-Type", "application/x-sentry-envelope")
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("relay status=%v err=%v", response.StatusCode, err)
	}
	response.Body.Close()
	if strings.Contains(string(forwarded), "relay-secret") || !strings.Contains(string(forwarded), "[Filtered]") {
		t.Fatalf("relay forwarded unsanitized body: %q", forwarded)
	}
}

func TestRelayMirrorsSentryEnvelopeWithoutBlockingPrimary(t *testing.T) {
	event := []byte(`{"event_id":"66666666666666666666666666666666","message":"dual-write"}`)
	envelope := testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(event))+`}`, event))
	mirrorReceived := make(chan []byte, 1)
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mirrorReceived <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer mirror.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted":1}`))
	}))
	defer primary.Close()
	relay := httptest.NewServer(NewRelayWithConfigAndMirror(primary.URL, mirror.URL, DefaultMaxEnvelopeBytes, "", "", DefaultPIIConfig()))
	defer relay.Close()
	request, _ := http.NewRequest(http.MethodPost, relay.URL+"/api/1/envelope?sentry_key=public", bytes.NewReader(envelope))
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("primary status=%v err=%v", response.StatusCode, err)
	}
	response.Body.Close()
	select {
	case body := <-mirrorReceived:
		if !strings.Contains(string(body), "dual-write") {
			t.Fatalf("mirror body=%q", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mirror did not receive the envelope")
	}
}

func TestRelayMirrorUsesDetachedContext(t *testing.T) {
	sourceContext, cancelSource := context.WithCancel(context.Background())
	source, err := http.NewRequestWithContext(sourceContext, http.MethodPost, "http://relay.test/api/1/envelope", bytes.NewReader([]byte("body")))
	if err != nil {
		t.Fatal(err)
	}
	mirrorContext, cancelMirror := context.WithCancel(context.Background())
	defer cancelMirror()
	mirrorRequest, err := newRelayRequestWithContext(mirrorContext, source, "http://mirror.test/api/1/envelope", []byte("body"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	cancelSource()
	select {
	case <-mirrorRequest.Context().Done():
		t.Fatal("mirror request was cancelled with the source request")
	default:
	}
}

func TestRelayDoesNotDuplicateCORSHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "https://upstream.invalid")
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	relay := httptest.NewServer(NewRelay(upstream.URL, DefaultMaxEnvelopeBytes, ""))
	defer relay.Close()
	event := []byte(`{"event_id":"66666666666666666666666666666666","message":"cors"}`)
	response, err := http.Post(relay.URL+"/api/1/envelope?sentry_key=public", "application/x-sentry-envelope", bytes.NewReader(testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(event))+`}`, event))))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if values := response.Header.Values("Access-Control-Allow-Origin"); len(values) != 1 || values[0] != "*" {
		t.Fatalf("cors headers=%v", values)
	}
	if response.Header.Get("X-Upstream") != "ok" {
		t.Fatalf("upstream header missing: %v", response.Header)
	}
}

func envelopePart(header string, payload []byte) []byte {
	return append(append([]byte(header), '\n'), append(payload, '\n')...)
}

func testEnvelope(parts ...[]byte) []byte {
	body := []byte("{}\n")
	for _, part := range parts {
		body = append(body, part...)
	}
	return body
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	result := make([]byte, 0, 8)
	for value > 0 {
		result = append([]byte{byte('0' + value%10)}, result...)
		value /= 10
	}
	return string(result)
}
