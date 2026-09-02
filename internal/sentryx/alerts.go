package sentryx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// EvaluateAlerts is intentionally storage-side and interval based. It keeps
// alert delivery out of the ingest transaction and makes retries safe through
// the cooldown timestamp.
func (p *PostgresStore) EvaluateAlerts(ctx context.Context) error {
	rules := p.ListAlertRules("")
	now := time.Now().UTC()
	for _, rule := range rules {
		if !rule.Enabled || rule.ProjectID == "" {
			continue
		}
		window := time.Duration(rule.WindowMinutes) * time.Minute
		if window <= 0 {
			window = time.Hour
		}
		if rule.LastTriggeredAt != nil && now.Sub(*rule.LastTriggeredAt) < time.Duration(rule.CooldownMinutes)*time.Minute {
			continue
		}
		triggered, value := p.alertTriggered(rule, now.Add(-window))
		if !triggered {
			continue
		}
		payload, _ := json.Marshal(map[string]any{"rule": rule, "project_id": rule.ProjectID, "value": value, "triggered_at": now})
		for _, action := range rule.Actions {
			if strings.EqualFold(action.Type, "webhook") && strings.HasPrefix(action.URL, "http") {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, action.URL, bytes.NewReader(payload))
				if err != nil {
					continue
				}
				req.Header.Set("Content-Type", "application/json")
				response, err := http.DefaultClient.Do(req)
				if err == nil {
					response.Body.Close()
				}
			}
		}
		_, _ = p.db.ExecContext(ctx, `UPDATE sentryx_alert_rules SET last_triggered_at=$1,updated_at=$1 WHERE id=$2`, now, rule.ID)
	}
	return nil
}

func (p *PostgresStore) alertTriggered(rule AlertRule, since time.Time) (bool, int64) {
	var value int64
	switch strings.ToLower(rule.Condition) {
	case "new_issue":
		_ = p.db.QueryRow(`SELECT count(*) FROM sentryx_issues WHERE project_id=$1 AND first_seen >= $2`, rule.ProjectID, since).Scan(&value)
	case "regression":
		_ = p.db.QueryRow(`SELECT count(*) FROM sentryx_issues WHERE project_id=$1 AND regression=true AND last_seen >= $2`, rule.ProjectID, since).Scan(&value)
	default: // count and spike both use event volume as a safe baseline.
		_ = p.db.QueryRow(`SELECT count(*) FROM sentryx_events WHERE project_id=$1 AND received_at >= $2`, rule.ProjectID, since).Scan(&value)
	}
	return value >= rule.Threshold, value
}
