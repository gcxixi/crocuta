package sentryx

import (
	"bytes"
	"context"
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
		if (rule.Condition == "new_issue" || rule.Condition == "regression") && int64(len(candidates)) < rule.Threshold {
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
			for _, action := range rule.Actions {
				if !strings.EqualFold(action.Type, "webhook") || !strings.HasPrefix(action.URL, "http") {
					continue
				}
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
			if delivered {
				_, err = p.db.ExecContext(ctx, `INSERT INTO sentryx_alert_notification_state (rule_id,issue_id,last_notified_at) VALUES ($1,$2,$3) ON CONFLICT (rule_id,issue_id) DO UPDATE SET last_notified_at=EXCLUDED.last_notified_at`, rule.ID, candidate.ID, now)
				if err != nil {
					failures = append(failures, fmt.Sprintf("rule %s cooldown: %v", rule.ID, err))
				}
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
	var last time.Time
	err := p.db.QueryRowContext(ctx, `SELECT last_notified_at FROM sentryx_alert_notification_state WHERE rule_id=$1 AND issue_id=$2`, rule.ID, issueID).Scan(&last)
	return err == nil && now.Sub(last) < time.Duration(rule.CooldownMinutes)*time.Minute
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
		query = `SELECT i.id,i.title,i.level,i.count,i.first_seen,i.latest_event_id,1 FROM sentryx_issues i WHERE i.project_id=$1 AND i.first_seen >= $2` + extra
	case "regression":
		query = `SELECT i.id,i.title,i.level,i.count,i.first_seen,i.latest_event_id,1 FROM sentryx_issues i WHERE i.project_id=$1 AND i.regression=true AND i.last_seen >= $2` + extra
	default:
		args = append(args, rule.Threshold)
		query = `SELECT i.id,i.title,i.level,i.count,i.first_seen,i.latest_event_id,count(e.event_id) FROM sentryx_issues i JOIN sentryx_events e ON e.issue_id=i.id AND e.received_at >= $2 WHERE i.project_id=$1` + extra + fmt.Sprintf(` GROUP BY i.id HAVING count(e.event_id) >= $%d`, len(args))
	}
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []alertCandidate{}
	for rows.Next() {
		var candidate alertCandidate
		if err := rows.Scan(&candidate.ID, &candidate.Title, &candidate.Level, &candidate.Count, &candidate.FirstSeen, &candidate.LatestEvent, &candidate.Value); err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for index := range result {
		var raw []byte
		if err := p.db.QueryRowContext(ctx, `SELECT canonical_json FROM sentryx_events WHERE project_id=$1 AND event_id=$2`, rule.ProjectID, result[index].LatestEvent).Scan(&raw); err != nil {
			continue
		}
		var event Event
		if json.Unmarshal(raw, &event) != nil {
			continue
		}
		frames := event.SymbolicatedFrames
		if len(frames) == 0 {
			frames = event.Frames
		}
		if len(frames) > 0 {
			frame := frames[len(frames)-1]
			result[index].StackTop = &frame
		}
	}
	return result, nil
}
