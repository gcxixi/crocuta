package e2e_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gitea.home.arpa/sundust/sentryx/internal/sentryx"
)

func TestNodeSDKThroughRelayAggregatesIssue(t *testing.T) {
	serverApp := sentryx.NewApp(nil)
	server := httptest.NewServer(serverApp.Handler())
	t.Cleanup(server.Close)

	relay := httptest.NewServer(sentryx.NewRelay(server.URL, sentryx.DefaultMaxEnvelopeBytes, "e2e-relay"))
	t.Cleanup(relay.Close)

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "node-sdk.mjs")
	cmd := exec.Command("node", script)
	cmd.Dir = root
	cmd.Env = os.Environ()
	// The SDK DSN parser uses the public key before '@'; use a valid local DSN.
	cmd.Env = append(cmd.Env, "SENTRYX_DSN=http://public@"+relay.URL[len("http://"):]+"/1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node SDK E2E failed: %v\n%s", err, output)
	}

	response, err := http.Get(server.URL + "/api/0/issues?project=1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("issues status = %d", response.StatusCode)
	}
	var issues []sentryx.Issue
	if err := json.NewDecoder(response.Body).Decode(&issues); err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want 1: %#v", len(issues), issues)
	}
	if issues[0].Count != 2 {
		t.Fatalf("issue count = %d, want 2", issues[0].Count)
	}
	if issues[0].ProjectID != "1" {
		t.Fatalf("project = %q, want 1", issues[0].ProjectID)
	}
}

func TestBrowserSDKThroughRelayAggregatesIssue(t *testing.T) {
	serverApp := sentryx.NewApp(nil)
	server := httptest.NewServer(serverApp.Handler())
	t.Cleanup(server.Close)

	relay := httptest.NewServer(sentryx.NewRelay(server.URL, sentryx.DefaultMaxEnvelopeBytes, "e2e-relay"))
	t.Cleanup(relay.Close)

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(root, "browser-sdk.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SENTRYX_DSN=http://public@"+relay.URL[len("http://"):]+"/1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser SDK E2E failed: %v\n%s", err, output)
	}

	response, err := http.Get(server.URL + "/api/0/issues?project=1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("issues status = %d", response.StatusCode)
	}
	var issues []sentryx.Issue
	if err := json.NewDecoder(response.Body).Decode(&issues); err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Count != 2 {
		t.Fatalf("browser issues = %#v, want one issue with count 2", issues)
	}
}

func TestNodeSDKSourceMapUploadAndSymbolication(t *testing.T) {
	serverApp := sentryx.NewApp(nil)
	server := httptest.NewServer(serverApp.Handler())
	t.Cleanup(server.Close)

	mapBody := `{"version":3,"file":"app.min.js","sources":["src/app.ts"],"names":["checkout"],"mappings":"AAAAA"}`
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile("file", "app.min.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(mapBody)); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("name", "app.min.js"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	upload, err := http.NewRequest(http.MethodPost, server.URL+"/api/0/projects/1/releases/sentryx-source-e2e%401.0.0/files/", &form)
	if err != nil {
		t.Fatal(err)
	}
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResponse, err := http.DefaultClient.Do(upload)
	if err != nil {
		t.Fatal(err)
	}
	uploadResponse.Body.Close()
	if uploadResponse.StatusCode != http.StatusOK {
		t.Fatalf("source map upload status = %d", uploadResponse.StatusCode)
	}

	relay := httptest.NewServer(sentryx.NewRelay(server.URL, sentryx.DefaultMaxEnvelopeBytes, "e2e-relay"))
	t.Cleanup(relay.Close)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(root, "sourcemap-sdk.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SENTRYX_DSN=http://public@"+relay.URL[len("http://"):]+"/1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("source map SDK E2E failed: %v\n%s", err, output)
	}

	response, err := http.Get(server.URL + "/api/0/events?project=1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var events []sentryx.Event
	if err := json.NewDecoder(response.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].SymbolicationStatus != "symbolicated" {
		t.Fatalf("events = %#v, want one symbolicated event", events)
	}
	if len(events[0].SymbolicatedFrames) != 1 || events[0].SymbolicatedFrames[0].Filename != "src/app.ts" {
		t.Fatalf("symbolicated frames = %#v, want src/app.ts", events[0].SymbolicatedFrames)
	}
	if events[0].SymbolicatedFrames[0].Function != "checkout" {
		t.Fatalf("symbolicated function = %q, want checkout", events[0].SymbolicatedFrames[0].Function)
	}
}
