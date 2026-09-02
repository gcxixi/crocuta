package sentryx

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPostgresAlertCandidatesAndNewIssueState(t *testing.T) {
	dsn := os.Getenv("SENTRYX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SENTRYX_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	projectID := "alert-project-" + suffix
	issueID := "alert-issue-" + suffix
	eventID := "alert-event-" + suffix
	ruleID := "alert-rule-" + suffix
	noActionRuleID := ruleID + "-no-action"
	now := time.Now().UTC()
	defer func() {
		_, _ = store.db.Exec(`DELETE FROM sentryx_alert_notification_state WHERE rule_id IN ($1,$2)`, ruleID, noActionRuleID)
		_, _ = store.db.Exec(`DELETE FROM sentryx_alert_rules WHERE id IN ($1,$2)`, ruleID, noActionRuleID)
		_, _ = store.db.Exec(`DELETE FROM sentryx_events WHERE project_id=$1`, projectID)
		_, _ = store.db.Exec(`DELETE FROM sentryx_issues WHERE project_id=$1`, projectID)
		_, _ = store.db.Exec(`DELETE FROM sentryx_ingest_jobs WHERE project_id=$1`, projectID)
	}()
	if _, err := store.db.Exec(`INSERT INTO sentryx_issues (id,project_id,title,level,count,first_seen,last_seen,latest_event_id,grouping_version,group_hash) VALUES ($1,$2,'boom','error',1,$3,$3,$4,2,$5)`, issueID, projectID, now, eventID, "hash-"+suffix); err != nil {
		t.Fatal(err)
	}
	event := Event{ProjectID: projectID, EventID: eventID, ReceivedAt: now, OccurredAt: now, Title: "boom", Frames: []StackFrame{{Filename: "app.js", Lineno: 42}}}
	if _, err := store.db.Exec(`INSERT INTO sentryx_events (project_id,event_id,issue_id,occurred_at,received_at,canonical_json) VALUES ($1,$2,$3,$4,$4,$5)`, projectID, eventID, issueID, now, mustJSON(event)); err != nil {
		t.Fatal(err)
	}
	for _, rule := range []AlertRule{
		{ProjectID: projectID, Condition: "new_issue", Filters: map[string]string{}},
		{ProjectID: projectID, Condition: "count", Threshold: 1, Filters: map[string]string{}},
	} {
		candidates, err := store.alertCandidates(ctx, rule, now.Add(-time.Minute))
		if err != nil || len(candidates) != 1 || candidates[0].StackTop == nil || candidates[0].StackTop.Filename != "app.js" {
			t.Fatalf("condition=%s candidates=%#v err=%v", rule.Condition, candidates, err)
		}
	}
	if _, err := store.db.Exec(`INSERT INTO sentryx_alert_rules (id,project_id,name,condition) VALUES ($1,$2,'new issue','new_issue')`, ruleID, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO sentryx_alert_notification_state (rule_id,issue_id,last_notified_at) VALUES ($1,$2,$3)`, ruleID, issueID, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !store.alertInCooldown(ctx, AlertRule{ID: ruleID, Condition: "new_issue", CooldownMinutes: 0}, issueID, now) {
		t.Fatal("new_issue must remain suppressed after its first successful notification")
	}
	if err := store.recordAlertAttempt(ctx, ruleID, issueID, now, false); err != nil {
		t.Fatal(err)
	}
	var failures int
	if err := store.db.QueryRow(`SELECT failures FROM sentryx_alert_notification_state WHERE rule_id=$1 AND issue_id=$2`, ruleID, issueID).Scan(&failures); err != nil || failures != 1 {
		t.Fatalf("failures=%d err=%v", failures, err)
	}
	if err := store.recordAlertAttempt(ctx, ruleID, issueID, now.Add(time.Minute), true); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT failures FROM sentryx_alert_notification_state WHERE rule_id=$1 AND issue_id=$2`, ruleID, issueID).Scan(&failures); err != nil || failures != 0 {
		t.Fatalf("reset failures=%d err=%v", failures, err)
	}
	if _, err := store.db.Exec(`INSERT INTO sentryx_alert_rules (id,project_id,name,condition,threshold,window_minutes,enabled) VALUES ($1,$2,'misconfigured','count',1,60,true)`, noActionRuleID, projectID); err != nil {
		t.Fatal(err)
	}
	if err := store.EvaluateAlerts(ctx); err == nil {
		t.Fatal("misconfigured rule should report its failed attempt")
	}
	if err := store.db.QueryRow(`SELECT failures FROM sentryx_alert_notification_state WHERE rule_id=$1 AND issue_id=$2`, noActionRuleID, issueID).Scan(&failures); err != nil || failures != 1 {
		t.Fatalf("no-action failures=%d err=%v", failures, err)
	}
	if err := store.EvaluateAlerts(ctx); err != nil {
		t.Fatalf("immediate retry should be suppressed by backoff: %v", err)
	}
	payload := []byte(`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","message":"rollup check"}`)
	body := testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(payload))+`}`, payload))
	if accepted, err := store.Ingest(projectID, "", body); err != nil || accepted != 1 {
		t.Fatalf("accepted=%d err=%v", accepted, err)
	}
	var rollups int
	if err := store.db.QueryRow(`SELECT count(*) FROM sentryx_issue_stats_hourly WHERE project_id=$1`, projectID).Scan(&rollups); err != nil || rollups != 0 {
		t.Fatalf("deprecated issue rollups=%d err=%v", rollups, err)
	}
}

func TestAlertRetryDelayIsExponentialAndCapped(t *testing.T) {
	if got := alertRetryDelay(1, 10); got != 30*time.Second {
		t.Fatalf("first retry=%s", got)
	}
	if got := alertRetryDelay(6, 2); got != 2*time.Minute {
		t.Fatalf("capped retry=%s", got)
	}
}
