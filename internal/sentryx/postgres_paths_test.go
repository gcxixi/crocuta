package sentryx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"
)

func postgresTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("SENTRYX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SENTRYX_TEST_DATABASE_URL is not set")
	}
	store, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func cleanupPostgresProject(t *testing.T, store *PostgresStore, projectID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = store.db.Exec(`DELETE FROM sentryx_alert_notification_state WHERE issue_id IN (SELECT id FROM sentryx_issues WHERE project_id=$1)`, projectID)
		_, _ = store.db.Exec(`DELETE FROM sentryx_alert_rules WHERE project_id=$1`, projectID)
		_, _ = store.db.Exec(`DELETE FROM sentryx_events WHERE project_id=$1`, projectID)
		_, _ = store.db.Exec(`DELETE FROM sentryx_signals WHERE project_id=$1`, projectID)
		_, _ = store.db.Exec(`DELETE FROM sentryx_issues WHERE project_id=$1`, projectID)
		_, _ = store.db.Exec(`DELETE FROM sentryx_ingest_jobs WHERE project_id=$1`, projectID)
	})
}

func TestPostgresGroupingVersionInheritance(t *testing.T) {
	store := postgresTestStore(t)
	projectID := fmt.Sprintf("grouping-%d", time.Now().UnixNano())
	cleanupPostgresProject(t, store, projectID)
	payload := func(id string) []byte {
		body, _ := json.Marshal(map[string]any{"event_id": id, "platform": "javascript", "message": "request 123 failed", "culprit": "assets/main.a1b2c3d4.js"})
		return testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(body))+`}`, body))
	}
	t.Setenv("SENTRYX_GROUPING_VERSION", "v1")
	if accepted, err := store.Ingest(projectID, "", payload("11111111111111111111111111111111")); err != nil || accepted != 1 {
		t.Fatalf("v1 accepted=%d err=%v", accepted, err)
	}
	t.Setenv("SENTRYX_GROUPING_VERSION", "v2")
	if accepted, err := store.Ingest(projectID, "", payload("22222222222222222222222222222222")); err != nil || accepted != 1 {
		t.Fatalf("v2 accepted=%d err=%v", accepted, err)
	}
	var issues, count, mappings int
	if err := store.db.QueryRow(`SELECT count(*),max(count) FROM sentryx_issues WHERE project_id=$1`, projectID).Scan(&issues, &count); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM sentryx_grouping_hashes WHERE project_id=$1`, projectID).Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if issues != 1 || count != 2 || mappings != 2 {
		t.Fatalf("issues=%d count=%d mappings=%d", issues, count, mappings)
	}
}

func TestPostgresIssuePaginationAllSorts(t *testing.T) {
	store := postgresTestStore(t)
	projectID := fmt.Sprintf("paging-%d", time.Now().UnixNano())
	cleanupPostgresProject(t, store, projectID)
	now := time.Now().UTC().Truncate(time.Second)
	for index := 0; index < 7; index++ {
		id := fmt.Sprintf("issue-%02d-%d", index, now.UnixNano())
		_, err := store.db.Exec(`INSERT INTO sentryx_issues (id,project_id,title,level,count,users,first_seen,last_seen,latest_event_id,grouping_version,group_hash) VALUES ($1,$2,$3,'error',$4,$5,$6,$6,$7,2,$8)`, id, projectID, "issue "+id, index%3+1, index%2, now, "event-"+id, "hash-"+id)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, sortName := range []string{"last_seen", "first_seen", "count", "users"} {
		seen := map[string]bool{}
		cursor := ""
		for {
			page := store.ListIssuesPage(QueryOptions{ProjectID: projectID, Sort: sortName, Limit: 2, Cursor: cursor})
			for _, issue := range page.Items {
				if seen[issue.ID] {
					t.Fatalf("sort=%s duplicate=%s", sortName, issue.ID)
				}
				seen[issue.ID] = true
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if len(seen) != 7 {
			t.Fatalf("sort=%s returned=%d", sortName, len(seen))
		}
	}
}

func TestPostgresIssueFiltersAndExactUsers(t *testing.T) {
	store := postgresTestStore(t)
	projectID := fmt.Sprintf("filters-%d", time.Now().UnixNano())
	cleanupPostgresProject(t, store, projectID)
	ingest := func(id, message, environment, release, browser, user string) {
		payload, _ := json.Marshal(map[string]any{"event_id": id, "message": message, "environment": environment, "release": release, "tags": map[string]string{"browser.name": browser}, "user": map[string]string{"id": user}})
		if accepted, err := store.Ingest(projectID, "", testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(payload))+`}`, payload))); err != nil || accepted != 1 {
			t.Fatalf("accepted=%d err=%v", accepted, err)
		}
	}
	ingest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "production boom", "production", "web@1", "Chrome", "alice")
	ingest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2", "production boom", "production", "web@1", "Chrome", "alice")
	ingest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3", "production boom", "production", "web@1", "Chrome", "bob")
	ingest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb1", "staging boom", "staging", "web@2", "Firefox", "carol")
	for name, options := range map[string]QueryOptions{
		"tag":         {ProjectID: projectID, Query: "browser.name:Chrome"},
		"environment": {ProjectID: projectID, Environment: "production"},
		"release":     {ProjectID: projectID, Release: "web@1"},
	} {
		page := store.ListIssuesPage(options)
		if len(page.Items) != 1 || page.Items[0].Title != "production boom" || page.Items[0].Users != 2 || page.Items[0].Count != 3 {
			t.Fatalf("%s page=%#v", name, page)
		}
		if detail, found := store.GetIssue(projectID, page.Items[0].ID); !found || detail.Users != 2 {
			t.Fatalf("%s detail=%#v found=%v", name, detail, found)
		}
	}
}

type recordingBlobStore struct{ deleted []string }

func (*recordingBlobStore) Put(context.Context, string, []byte) error   { return nil }
func (*recordingBlobStore) Get(context.Context, string) ([]byte, error) { return nil, nil }
func (s *recordingBlobStore) Delete(_ context.Context, key string) error {
	s.deleted = append(s.deleted, key)
	return nil
}

func TestPostgresCleanupIsBoundedAndReturnsBlobKeys(t *testing.T) {
	store := postgresTestStore(t)
	projectID := fmt.Sprintf("cleanup-%d", time.Now().UnixNano())
	cleanupPostgresProject(t, store, projectID)
	_, err := store.db.Exec(`INSERT INTO sentryx_signals (id,project_id,kind,received_at,schema_version,size,blob_key,payload) SELECT $1||n,$1,'replay_recording',now()-interval '3 days',1,1,'cleanup/'||n,'{}'::jsonb FROM generate_series(1,5001) n`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	blobs := &recordingBlobStore{}
	store.SetBlobStore(blobs)
	deleted, err := store.CleanupExpired(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.db.QueryRow(`SELECT count(*) FROM sentryx_signals WHERE project_id=$1`, projectID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	sort.Strings(blobs.deleted)
	if deleted != 5000 || remaining != 1 || len(blobs.deleted) != 5000 || blobs.deleted[0] == "" {
		t.Fatalf("deleted=%d remaining=%d blob keys=%d", deleted, remaining, len(blobs.deleted))
	}
}
