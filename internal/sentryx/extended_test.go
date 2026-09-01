package sentryx

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
	body := testEnvelope(
		envelopePart(`{"type":"event","length":`+itoa(len(eventBody))+`}`, eventBody),
		envelopePart(`{"type":"client_report","length":`+itoa(len(reportBody))+`}`, reportBody),
		envelopePart(`{"type":"attachment","length":`+itoa(len(attachmentBody)+0)+`,"filename":"console.log","content_type":"text/plain","event_id":"11111111111111111111111111111111"}`, attachmentBody),
		envelopePart(`{"type":"transaction","length":`+itoa(len(signalBody))+`,"event_id":"22222222222222222222222222222222"}`, signalBody),
	)
	accepted, err := store.Ingest("1", "public", body)
	if err != nil || accepted != 4 {
		t.Fatalf("accepted=%d err=%v", accepted, err)
	}
	events := store.ListEvents("1", "")
	if len(events) != 1 || events[0].User == nil || events[0].User.IPAddress != "[Filtered]" {
		t.Fatalf("events=%#v", events)
	}
	if events[0].Request == nil || events[0].Request.Headers["Authorization"] != "[Filtered]" {
		t.Fatalf("request=%#v", events[0].Request)
	}
	if len(store.ListClientReports("1")) != 1 || len(store.ListAttachments("1", "11111111111111111111111111111111")) != 1 || len(store.ListSignals("1", "transaction")) != 1 {
		t.Fatalf("extended items missing")
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
