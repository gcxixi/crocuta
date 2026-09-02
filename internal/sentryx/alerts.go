package sentryx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type alertCandidate struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Level       string      `json:"level"`
	Count       int64       `json:"count"`
	FirstSeen   time.Time   `json:"first_seen"`
	LatestEvent string      `json:"latest_event_id"`
	Value       int64       `json:"value"`
	StackTop    *StackFrame `json:"stack_top,omitempty"`
}

var alertHTTPClient = &http.Client{Timeout: 10 * time.Second}

// EvaluateAlerts serializes evaluators with a PostgreSQL advisory lock and
// tracks cooldown per (rule, issue), preventing one noisy issue from muting
// unrelated failures. It runs outside the ingest worker loop.
func (p *PostgresStore) EvaluateAlerts(ctx context.Context) error {
	connection, err := p.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	var locked bool
	if err := connection.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('sentryx-alert-evaluator'))`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer connection.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('sentryx-alert-evaluator'))`)

	rules := p.ListAlertRules("")
	now := time.Now().UTC()
	var failures []string
	for _, rule := range rules {
		if !rule.Enabled || rule.ProjectID == "" {
			continue
		}
		window := time.Duration(rule.WindowMinutes) * time.Minute
		if window <= 0 {
			window = time.Hour
		}
		candidates, err := p.alertCandidates(ctx, rule, now.Add(-window))
		if err != nil {
			failures = append(failures, fmt.Sprintf("rule %s query: %v", rule.ID, err))
			continue
		}
		for _, candidate := range candidates {
			if p.alertInCooldown(ctx, rule, candidate.ID, now) {
				continue
			}
			payload := map[string]any{"rule": rule, "project_id": rule.ProjectID, "issue": candidate, "triggered_at": now}
			if base := strings.TrimRight(os.Getenv("SENTRYX_UI_BASE_URL"), "/"); base != "" {
				payload["issue_url"] = base + "/issues/" + candidate.ID
			}
			encoded, _ := json.Marshal(payload)
			delivered := false
			validActions := 0
			for _, action := range rule.Actions {
				if !strings.EqualFold(action.Type, "webhook") || !strings.HasPrefix(action.URL, "http") {
					continue
				}
				validActions++
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, action.URL, bytes.NewReader(encoded))
				if err == nil {
					req.Header.Set("Content-Type", "application/json")
					var response *http.Response
					response, err = alertHTTPClient.Do(req)
					if response != nil {
						response.Body.Close()
						if response.StatusCode >= 300 {
							err = fmt.Errorf("status %s", response.Status)
						}
					}
				}
				if err != nil {
					DefaultMetrics.Inc("sentryx_alert_delivery_errors_total", map[string]string{"type": "webhook"})
					failures = append(failures, fmt.Sprintf("rule %s issue %s: %v", rule.ID, candidate.ID, err))
					continue
				}
				delivered = true
			}
			if validActions == 0 {
				DefaultMetrics.Inc("sentryx_alert_delivery_errors_total", map[string]string{"type": "configuration"})
				failures = append(failures, fmt.Sprintf("rule %s issue %s: no valid delivery action", rule.ID, candidate.ID))
			}
			if err := p.recordAlertAttempt(ctx, rule.ID, candidate.ID, now, delivered); err != nil {
				failures = append(failures, fmt.Sprintf("rule %s attempt state: %v", rule.ID, err))
			}
			if delivered {
				_, _ = p.db.ExecContext(ctx, `UPDATE sentryx_alert_rules SET last_triggered_at=$1,updated_at=$1 WHERE id=$2`, now, rule.ID)
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("alert evaluation: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (p *PostgresStore) alertInCooldown(ctx context.Context, rule AlertRule, issueID string, now time.Time) bool {
	var lastNotified, lastAttempted sql.NullTime
	var failures int
	err := p.db.QueryRowContext(ctx, `SELECT last_notified_at,last_attempted_at,failures FROM sentryx_alert_notification_state WHERE rule_id=$1 AND issue_id=$2`, rule.ID, issueID).Scan(&lastNotified, &lastAttempted, &failures)
	if err != nil {
		return false
	}
	if strings.EqualFold(rule.Condition, "new_issue") && lastNotified.Valid {
		return true
	}
	if failures > 0 && lastAttempted.Valid && now.Before(lastAttempted.Time.Add(alertRetryDelay(failures, rule.CooldownMinutes))) {
		return true
	}
	return lastNotified.Valid && now.Sub(lastNotified.Time) < time.Duration(rule.CooldownMinutes)*time.Minute
}

func alertRetryDelay(failures, cooldownMinutes int) time.Duration {
	if failures < 1 {
		return 0
	}
	delay := 30 * time.Second
	for attempt := 1; attempt < failures && delay < time.Hour; attempt++ {
		delay *= 2
	}
	capDelay := time.Hour
	if cooldownMinutes > 0 && time.Duration(cooldownMinutes)*time.Minute < capDelay {
		capDelay = time.Duration(cooldownMinutes) * time.Minute
	}
	if delay > capDelay {
		return capDelay
	}
	return delay
}

func (p *PostgresStore) recordAlertAttempt(ctx context.Context, ruleID, issueID string, now time.Time, delivered bool) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO sentryx_alert_notification_state (rule_id,issue_id,last_notified_at,last_attempted_at,failures)
		VALUES ($1,$2,CASE WHEN $4 THEN $3::timestamptz ELSE NULL END,$3::timestamptz,CASE WHEN $4 THEN 0 ELSE 1 END)
		ON CONFLICT (rule_id,issue_id) DO UPDATE SET
		  last_notified_at=CASE WHEN $4 THEN $3::timestamptz ELSE sentryx_alert_notification_state.last_notified_at END,
		  last_attempted_at=$3::timestamptz,
		  failures=CASE WHEN $4 THEN 0 ELSE sentryx_alert_notification_state.failures+1 END`, ruleID, issueID, now, delivered)
	return err
}

func (p *PostgresStore) alertCandidates(ctx context.Context, rule AlertRule, since time.Time) ([]alertCandidate, error) {
	args := []any{rule.ProjectID, since}
	filters := []string{}
	if level := strings.TrimSpace(rule.Filters["level"]); level != "" {
		args = append(args, level)
		filters = append(filters, fmt.Sprintf("i.level=$%d", len(args)))
	}
	for _, key := range []string{"environment", "release"} {
		if value := strings.TrimSpace(rule.Filters[key]); value != "" {
			args = append(args, value)
			filters = append(filters, fmt.Sprintf("EXISTS (SELECT 1 FROM sentryx_events fe WHERE fe.issue_id=i.id AND fe.received_at >= $2 AND fe.canonical_json->>'%s'=$%d)", key, len(args)))
		}
	}
	extra := ""
	if len(filters) > 0 {
		extra = " AND " + strings.Join(filters, " AND ")
	}
	var query string
	switch strings.ToLower(rule.Condition) {
	case "new_issue":
		query = `SELECT i.id,i.title,i.level,i.count,i.first_seen,i.latest_event_id,1,COALESCE(latest.canonical_json,'{}'::jsonb) FROM sentryx_issues i LEFT JOIN sentryx_events latest ON latest.project_id=i.project_id AND latest.event_id=i.latest_event_id WHERE i.project_id=$1 AND i.first_seen >= $2` + extra
	case "regression":
		query = `SELECT i.id,i.title,i.level,i.count,i.first_seen,i.latest_event_id,1,COALESCE(latest.canonical_json,'{}'::jsonb) FROM sentryx_issues i LEFT JOIN sentryx_events latest ON latest.project_id=i.project_id AND latest.event_id=i.latest_event_id WHERE i.project_id=$1 AND i.regression=true AND i.last_seen >= $2` + extra
	default:
		args = append(args, rule.Threshold)
		query = `WITH candidates AS (SELECT i.id,count(e.event_id) AS value FROM sentryx_issues i JOIN sentryx_events e ON e.issue_id=i.id AND e.received_at >= $2 WHERE i.project_id=$1` + extra + fmt.Sprintf(` GROUP BY i.id HAVING count(e.event_id) >= $%d) SELECT i.id,i.title,i.level,i.count,i.first_seen,i.latest_event_id,c.value,COALESCE(latest.canonical_json,'{}'::jsonb) FROM candidates c JOIN sentryx_issues i ON i.id=c.id LEFT JOIN sentryx_events latest ON latest.project_id=i.project_id AND latest.event_id=i.latest_event_id`, len(args))
	}
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []alertCandidate{}
	for rows.Next() {
		var candidate alertCandidate
		var raw []byte
		if err := rows.Scan(&candidate.ID, &candidate.Title, &candidate.Level, &candidate.Count, &candidate.FirstSeen, &candidate.LatestEvent, &candidate.Value, &raw); err != nil {
			return nil, err
		}
		var event Event
		if json.Unmarshal(raw, &event) == nil {
			frames := event.SymbolicatedFrames
			if len(frames) == 0 {
				frames = event.Frames
			}
			if len(frames) > 0 {
				frame := frames[len(frames)-1]
				candidate.StackTop = &frame
			}
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
