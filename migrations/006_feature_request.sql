-- Feature-request baseline: lifecycle state, retention, rollups, and alert rules.
ALTER TABLE sentryx_ingest_jobs ALTER COLUMN payload DROP NOT NULL;

ALTER TABLE sentryx_issues ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'unresolved';
ALTER TABLE sentryx_issues ADD COLUMN IF NOT EXISTS status_changed_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE sentryx_issues ADD COLUMN IF NOT EXISTS resolved_in_release TEXT;
ALTER TABLE sentryx_issues ADD COLUMN IF NOT EXISTS ignore_until TIMESTAMPTZ;
ALTER TABLE sentryx_issues ADD COLUMN IF NOT EXISTS regression BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS sentryx_issues_project_status_seen_idx
  ON sentryx_issues (project_id, status, last_seen DESC);

ALTER TABLE sentryx_control_projects ADD COLUMN IF NOT EXISTS retention_days INTEGER NOT NULL DEFAULT 30;

CREATE TABLE IF NOT EXISTS sentryx_issue_stats_hourly (
  project_id TEXT NOT NULL,
  issue_id TEXT NOT NULL REFERENCES sentryx_issues(id) ON DELETE CASCADE,
  bucket TIMESTAMPTZ NOT NULL,
  event_count BIGINT NOT NULL DEFAULT 0,
  user_count BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, issue_id, bucket)
);
CREATE INDEX IF NOT EXISTS sentryx_issue_stats_project_bucket_idx
  ON sentryx_issue_stats_hourly (project_id, bucket DESC);

CREATE TABLE IF NOT EXISTS sentryx_alert_rules (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  name TEXT NOT NULL,
  condition TEXT NOT NULL,
  threshold BIGINT NOT NULL DEFAULT 1,
  window_minutes INTEGER NOT NULL DEFAULT 60,
  filters JSONB NOT NULL DEFAULT '{}'::jsonb,
  actions JSONB NOT NULL DEFAULT '[]'::jsonb,
  cooldown_minutes INTEGER NOT NULL DEFAULT 30,
  enabled BOOLEAN NOT NULL DEFAULT true,
  last_triggered_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sentryx_alert_rules_project_enabled_idx
  ON sentryx_alert_rules (project_id, enabled);
