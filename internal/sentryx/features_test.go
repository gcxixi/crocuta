package sentryx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestItemPolicyFiltersAndKeepsEnvelopeValid(t *testing.T) {
	event := []byte(`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","message":"keep"}`)
	transaction := []byte(`{"transaction":"drop"}`)
	body := testEnvelope(
		envelopePart(`{"type":"event","length":`+itoa(len(event))+`}`, event),
		envelopePart(`{"type":"transaction","length":`+itoa(len(transaction))+`}`, transaction),
	)
	filtered, dropped, err := ApplyItemPolicy(body, ParseItemPolicy("transaction:drop"))
	if err != nil || dropped != 1 {
		t.Fatalf("filtered=%q dropped=%d err=%v", filtered, dropped, err)
	}
	items, err := parseEnvelope(filtered)
	if err != nil || len(items) != 1 || items[0].Type != "event" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestItemPolicyPreservesEnvelopeAndItemHeaders(t *testing.T) {
	event := []byte(`{"event_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","message":"keep"}`)
	transaction := []byte(`{"transaction":"drop"}`)
	body := []byte(`{"event_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sent_at":"2026-09-02T00:00:00Z","trace":{"trace_id":"abc"}}` + "\n" +
		`{"type":"event","length":` + itoa(len(event)) + `,"attachment_type":"event.minidump"}` + "\n" + string(event) + "\n" +
		`{"type":"transaction","length":` + itoa(len(transaction)) + `}` + "\n" + string(transaction) + "\n")
	filtered, dropped, err := ApplyItemPolicy(body, ParseItemPolicy("transaction:drop"))
	if err != nil || dropped != 1 {
		t.Fatalf("dropped=%d err=%v", dropped, err)
	}
	firstLine, _, _ := nextLine(filtered, 0)
	if !strings.Contains(string(firstLine), `"sent_at"`) || !strings.Contains(string(firstLine), `"trace"`) {
		t.Fatalf("envelope header was not preserved: %s", firstLine)
	}
	items, err := parseEnvelope(filtered)
	if err != nil || len(items) != 1 || items[0].Header["attachment_type"] != "event.minidump" {
		t.Fatalf("item headers were not preserved: %#v err=%v", items, err)
	}
	cleaned, err := ScrubEnvelope(filtered, DefaultPIIConfig())
	if err != nil {
		t.Fatal(err)
	}
	items, err = parseEnvelope(cleaned)
	if err != nil || items[0].Header["attachment_type"] != "event.minidump" {
		t.Fatalf("PII pass dropped item headers: %#v err=%v", items, err)
	}
}

func TestIssueLifecyclePaginationAndAnalytics(t *testing.T) {
	store := NewStore()
	for index, message := range []string{"first 100", "second 200"} {
		payload := map[string]any{"event_id": strings.Repeat(string(rune('a'+index)), 32), "message": message, "tags": map[string]string{"browser": "chrome"}, "user": map[string]string{"id": "u"}}
		body, _ := json.Marshal(payload)
		if _, err := store.Ingest("1", "public", testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(body))+`}`, body))); err != nil {
			t.Fatal(err)
		}
	}
	page := store.ListIssuesPage(QueryOptions{ProjectID: "1", Limit: 1})
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("page=%#v", page)
	}
	next := store.ListIssuesPage(QueryOptions{ProjectID: "1", Limit: 1, Cursor: page.NextCursor})
	if len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatalf("next=%#v", next)
	}
	issue, err := store.SetIssueStatus(page.Items[0].ID, "resolved", "web@1")
	if err != nil || issue.Status != "resolved" {
		t.Fatalf("issue=%#v err=%v", issue, err)
	}
	series := store.IssueSeries("1", issue.ID, issue.FirstSeen.Add(-1), issue.LastSeen.Add(1), 0)
	if len(series) == 0 || series[0].Count != 1 {
		t.Fatalf("series=%#v", series)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/0/issues?project=1&limit=1", nil)
	response := httptest.NewRecorder()
	NewApp(store).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Next-Cursor") == "" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/0/issues/"+issue.ID+"/series?project=1&resolution=24h", nil)
	response = httptest.NewRecorder()
	NewApp(store).Handler().ServeHTTP(response, request)
	var apiSeries []SeriesPoint
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &apiSeries) != nil || len(apiSeries) != 1 {
		t.Fatalf("series route status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/0/issues/"+issue.ID+"/tags/browser/?project=1", nil)
	response = httptest.NewRecorder()
	NewApp(store).Handler().ServeHTTP(response, request)
	var apiTags []TagValueCount
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &apiTags) != nil || len(apiTags) != 1 {
		t.Fatalf("tags route status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHashedManagementToken(t *testing.T) {
	app := NewApp(nil)
	app.APITokenHashes = ParseAPITokenHashes(HashAPIToken("secret") + ":1")
	request := httptest.NewRequest(http.MethodGet, "/api/0/organizations", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestMemoryAlertRules(t *testing.T) {
	store := NewStore()
	rule := store.CreateAlertRule(AlertRule{ProjectID: "1", Name: "errors", Condition: "count", Actions: []AlertAction{{Type: "webhook", URL: "https://example.invalid"}}})
	if rule.ID == "" || len(store.ListAlertRules("1")) != 1 {
		t.Fatalf("rule=%#v", rule)
	}
	rule.Name = "updated"
	if updated, ok := store.UpdateAlertRule(rule); !ok || updated.Name != "updated" {
		t.Fatalf("updated=%#v ok=%v", updated, ok)
	}
	if !store.DeleteAlertRule("1", rule.ID) || len(store.ListAlertRules("1")) != 0 {
		t.Fatal("rule was not deleted")
	}
}

func TestProjectKeyRotationAndGroupingVersions(t *testing.T) {
	control := NewMemoryControlPlane()
	project, err := control.CreateProject("default", "Web", "web", "javascript")
	if err != nil {
		t.Fatal(err)
	}
	keys, ok := control.(ProjectKeyStore)
	if !ok {
		t.Fatal("memory control does not implement project key store")
	}
	created, err := keys.CreateProjectKey("default", project.Slug, "rotation")
	if err != nil || created.Public == "" || created.Secret == "" {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	if len(keys.ListProjectKeys("default", project.Slug)) != 2 {
		t.Fatal("expected two active keys")
	}
	if err := keys.RevokeProjectKey("default", project.Slug, created.ID); err != nil {
		t.Fatal(err)
	}
	if len(keys.ListProjectKeys("default", project.Slug)) != 1 {
		t.Fatal("expected original key to remain")
	}
	event := Event{Platform: "javascript", Message: "request 123 failed", Culprit: "assets/main.a1b2c3d4.js"}
	v1 := GroupingHashForVersion(event, "v1")
	v2 := GroupingHashForVersion(event, "v2")
	if v1 == v2 {
		t.Fatal("versioned grouping hashes must differ")
	}
	if got := normalizePath("https://cdn.test/assets/main.a1b2c3d4.js"); got != "/assets/main" {
		t.Fatalf("normalized path=%q", got)
	}
	if facade, decade := normalizePath("theme.facade.js"), normalizePath("theme.decade.js"); facade == decade {
		t.Fatalf("ordinary words were treated as content hashes: %q %q", facade, decade)
	}
}

func TestIssue2DistinctUsersDerivedTagsAndResolution(t *testing.T) {
	store := NewStore()
	for index := 0; index < 5; index++ {
		id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + itoa(index)
		payload := map[string]any{
			"event_id": id, "platform": "javascript", "message": "boom", "release": "web@1", "environment": "production",
			"user":     map[string]any{"id": "alice"},
			"contexts": map[string]any{"browser": map[string]any{"name": "Chrome", "version": "120"}, "os": map[string]any{"name": "macOS"}},
		}
		encoded, _ := json.Marshal(payload)
		if _, err := store.Ingest("1", "key", testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(encoded))+`}`, encoded))); err != nil {
			t.Fatal(err)
		}
	}
	issues := store.ListIssues("1")
	if len(issues) != 1 {
		t.Fatalf("issues=%d", len(issues))
	}
	series := store.IssueSeries("1", issues[0].ID, time.Time{}, time.Time{}, 24*time.Hour)
	if len(series) != 1 || series[0].Count != 5 || series[0].Users != 1 {
		t.Fatalf("series=%#v", series)
	}
	for key, want := range map[string]string{"browser.name": "Chrome", "browser.version": "120", "os.name": "macOS", "release": "web@1", "environment": "production", "user.id": "alice"} {
		values := store.IssueTagValues("1", issues[0].ID, key, time.Time{}, time.Time{}, 10)
		if len(values) != 1 || values[0].Value != want || values[0].Count != 5 {
			t.Fatalf("tag %s=%#v", key, values)
		}
	}
	stats := store.ProjectStats("1", issues[0].FirstSeen.Add(-time.Second), issues[0].LastSeen.Add(time.Second))
	if stats["errors"] != 5 || stats["issues"] != 1 || stats["users"] != 1 {
		t.Fatalf("stats=%#v", stats)
	}
}

func TestGroupingFramesPreferApplicationCode(t *testing.T) {
	frames := []StackFrame{
		{Filename: "/src/checkout.ts", Function: "submit", InApp: true},
		{Filename: "/node_modules/axios/index.js", Function: "dispatch", InApp: false},
	}
	selected := groupingFrames(frames)
	if len(selected) != 1 || selected[0].Filename != "/src/checkout.ts" {
		t.Fatalf("selected=%#v", selected)
	}
}
