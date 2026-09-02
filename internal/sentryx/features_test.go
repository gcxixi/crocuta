package sentryx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
}
