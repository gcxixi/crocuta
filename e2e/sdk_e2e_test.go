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
	"strings"
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

func TestFrameworkSDKsThroughRelay(t *testing.T) {
	serverApp := sentryx.NewApp(nil)
	server := httptest.NewServer(serverApp.Handler())
	t.Cleanup(server.Close)

	relay := httptest.NewServer(sentryx.NewRelay(server.URL, sentryx.DefaultMaxEnvelopeBytes, "e2e-relay"))
	t.Cleanup(relay.Close)

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(root, "framework-sdk.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SENTRYX_DSN=http://public@"+relay.URL[len("http://"):]+"/1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("framework SDK E2E failed: %v\n%s", err, output)
	}

	response, err := http.Get(server.URL + "/api/0/events?project=1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", response.StatusCode)
	}
	var events []sentryx.Event
	if err := json.NewDecoder(response.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("framework events = %d, want 3: %#v", len(events), events)
	}
	seen := map[string]bool{}
	for _, event := range events {
		seen[event.Release] = true
		if event.Platform != "javascript" {
			t.Fatalf("framework event platform = %q, want javascript", event.Platform)
		}
	}
	for _, release := range []string{"sentryx-react-e2e@1.0.0", "sentryx-vue-e2e@1.0.0", "sentryx-angular-e2e@1.0.0"} {
		if !seen[release] {
			t.Fatalf("missing framework release %q in %#v", release, seen)
		}
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
	cmd.Env = append(os.Environ(),
		"SENTRYX_DSN=http://public@"+relay.URL[len("http://"):]+"/1",
		"SENTRYX_BASE_URL="+server.URL,
	)
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

func TestRealSourceMapAndBreadcrumbsSDKThroughRelay(t *testing.T) {
	serverApp := sentryx.NewApp(nil)
	server := httptest.NewServer(serverApp.Handler())
	t.Cleanup(server.Close)

	relay := httptest.NewServer(sentryx.NewRelay(server.URL, sentryx.DefaultMaxEnvelopeBytes, "e2e-relay"))
	t.Cleanup(relay.Close)

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("node", filepath.Join(root, "real-sourcemap-sdk.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"SENTRYX_DSN=http://public@"+relay.URL[len("http://"):]+"/1",
		"SENTRYX_BASE_URL="+server.URL,
		"SENTRYX_RELEASE=sentryx-real-sourcemap-e2e@1.0.0",
		"SENTRYX_PROJECT=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real sourcemap SDK E2E failed: %v\n%s", err, output)
	}

	// 1. Check Events API
	response, err := http.Get(server.URL + "/api/0/events?project=1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var events []sentryx.Event
	if err := json.NewDecoder(response.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events count = %d, want 1: %#v", len(events), events)
	}
	event := events[0]
	if event.SymbolicationStatus != "symbolicated" {
		t.Fatalf("symbolication_status = %q, want symbolicated", event.SymbolicationStatus)
	}

	// Verify symbolicated stack frames from real compiled typescript source
	if len(event.SymbolicatedFrames) == 0 {
		t.Fatalf("expected symbolicated frames, got empty: %#v", event)
	}
	topFrame := event.SymbolicatedFrames[len(event.SymbolicatedFrames)-1]
	if topFrame.Filename != "src/checkout-flow.ts" {
		t.Fatalf("symbolicated filename = %q, want src/checkout-flow.ts", topFrame.Filename)
	}
	if topFrame.ContextLine == "" {
		t.Fatalf("expected non-empty context_line on symbolicated frame: %#v", topFrame)
	}
	if !strings.Contains(topFrame.ContextLine, "throw new Error") {
		t.Fatalf("expected context_line to contain 'throw new Error', got %q", topFrame.ContextLine)
	}
	if len(topFrame.PreContext) == 0 {
		t.Fatalf("expected pre_context to be populated, got empty: %#v", topFrame)
	}

	// 2. Check Breadcrumbs (测试 breadcrumbs)
	if len(event.Breadcrumbs) < 3 {
		t.Fatalf("breadcrumbs count = %d, want >= 3: %#v", len(event.Breadcrumbs), event.Breadcrumbs)
	}

	hasAuth := false
	hasClick := false
	hasHTTP := false
	for _, crumb := range event.Breadcrumbs {
		switch crumb.Category {
		case "auth":
			hasAuth = true
			if !strings.Contains(crumb.Message, "customer@example.com") {
				t.Fatalf("auth breadcrumb message = %q, want email", crumb.Message)
			}
		case "ui.click":
			hasClick = true
			if !strings.Contains(crumb.Message, "submit payment") {
				t.Fatalf("ui.click breadcrumb message = %q", crumb.Message)
			}
			if crumb.Data == nil || crumb.Data["cart_id"] != "cart-998877" {
				t.Fatalf("ui.click breadcrumb data = %#v", crumb.Data)
			}
		case "http":
			hasHTTP = true
			if !strings.Contains(crumb.Message, "charges") {
				t.Fatalf("http breadcrumb message = %q", crumb.Message)
			}
			if crumb.Data == nil || crumb.Data["error_code"] != "insufficient_funds" {
				t.Fatalf("http breadcrumb data = %#v", crumb.Data)
			}
		}
	}

	if !hasAuth || !hasClick || !hasHTTP {
		t.Fatalf("missing expected breadcrumbs (auth=%v, click=%v, http=%v): %#v", hasAuth, hasClick, hasHTTP, event.Breadcrumbs)
	}

	// 3. Check Issues API
	issueResp, err := http.Get(server.URL + "/api/0/issues?project=1")
	if err != nil {
		t.Fatal(err)
	}
	defer issueResp.Body.Close()
	var issues []sentryx.Issue
	if err := json.NewDecoder(issueResp.Body).Decode(&issues); err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Count != 1 {
		t.Fatalf("issues = %#v, want 1 issue with count 1", issues)
	}
}
