-- Correctness and operational indexes for GitHub issue #2.
CREATE TABLE IF NOT EXISTS sentryx_alert_notification_state (
  rule_id TEXT NOT NULL REFERENCES sentryx_alert_rules(id) ON DELETE CASCADE,
  issue_id TEXT NOT NULL REFERENCES sentryx_issues(id) ON DELETE CASCADE,
  last_notified_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (rule_id, issue_id)
);

CREATE INDEX IF NOT EXISTS sentryx_issues_project_count_idx
  ON sentryx_issues (project_id, count DESC, id DESC);
CREATE INDEX IF NOT EXISTS sentryx_events_issue_only_received_idx
  ON sentryx_events (issue_id, received_at DESC);
CREATE INDEX IF NOT EXISTS sentryx_events_dimensions_idx
  ON sentryx_events USING GIN ((canonical_json->'tags'));

CREATE TABLE IF NOT EXISTS sentryx_issue_tag_values_hourly (
  project_id TEXT NOT NULL,
  issue_id TEXT NOT NULL REFERENCES sentryx_issues(id) ON DELETE CASCADE,
  bucket TIMESTAMPTZ NOT NULL,
  tag_key TEXT NOT NULL,
  tag_value TEXT NOT NULL,
  event_count BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, issue_id, bucket, tag_key, tag_value)
);
CREATE INDEX IF NOT EXISTS sentryx_issue_tag_values_lookup_idx
  ON sentryx_issue_tag_values_hourly (project_id, issue_id, tag_key, bucket DESC);

INSERT INTO sentryx_issue_tag_values_hourly
  (project_id, issue_id, bucket, tag_key, tag_value, event_count)
SELECT e.project_id, e.issue_id, date_trunc('hour', e.received_at), tags.key, tags.value, count(*)
FROM sentryx_events e
CROSS JOIN LATERAL jsonb_each_text(COALESCE(e.canonical_json->'tags', '{}'::jsonb)) AS tags(key, value)
GROUP BY e.project_id, e.issue_id, date_trunc('hour', e.received_at), tags.key, tags.value
ON CONFLICT (project_id, issue_id, bucket, tag_key, tag_value)
DO UPDATE SET event_count=EXCLUDED.event_count;

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS sentryx_issues_title_trgm_idx
  ON sentryx_issues USING GIN (title gin_trgm_ops);

-- user_count in the hourly table is retained for backwards compatibility but
-- is no longer exposed as distinct users. API cardinality is computed exactly
-- from bounded event windows.
COMMENT ON COLUMN sentryx_issue_stats_hourly.user_count IS
  'Deprecated count of events carrying a user identity; not distinct users.';
