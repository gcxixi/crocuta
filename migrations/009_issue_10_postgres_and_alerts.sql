-- Issue #10: durable alert attempts and low-cost exact affected-user counts.
ALTER TABLE sentryx_alert_notification_state
  ALTER COLUMN last_notified_at DROP NOT NULL;
ALTER TABLE sentryx_alert_notification_state
  ADD COLUMN IF NOT EXISTS last_attempted_at TIMESTAMPTZ;
ALTER TABLE sentryx_alert_notification_state
  ADD COLUMN IF NOT EXISTS failures INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS sentryx_issue_users (
  project_id TEXT NOT NULL,
  issue_id TEXT NOT NULL REFERENCES sentryx_issues(id) ON DELETE CASCADE,
  user_key TEXT NOT NULL,
  first_seen TIMESTAMPTZ NOT NULL,
  last_seen TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (project_id, issue_id, user_key)
);
CREATE INDEX IF NOT EXISTS sentryx_issue_users_issue_idx
  ON sentryx_issue_users (issue_id, user_key);

INSERT INTO sentryx_issue_users (project_id, issue_id, user_key, first_seen, last_seen)
SELECT project_id, issue_id,
       CASE
         WHEN COALESCE(canonical_json->'user'->>'id','') <> '' THEN 'id:'||(canonical_json->'user'->>'id')
         WHEN COALESCE(canonical_json->'user'->>'email','') <> '' THEN 'email:'||lower(canonical_json->'user'->>'email')
       END,
       min(received_at), max(received_at)
FROM sentryx_events
WHERE issue_id IS NOT NULL
  AND (COALESCE(canonical_json->'user'->>'id','') <> '' OR COALESCE(canonical_json->'user'->>'email','') <> '')
GROUP BY project_id, issue_id, 3
ON CONFLICT (project_id, issue_id, user_key) DO UPDATE
SET first_seen=LEAST(sentryx_issue_users.first_seen, EXCLUDED.first_seen),
    last_seen=GREATEST(sentryx_issue_users.last_seen, EXCLUDED.last_seen);

ALTER TABLE sentryx_issues ADD COLUMN IF NOT EXISTS users BIGINT NOT NULL DEFAULT 0;
UPDATE sentryx_issues i
SET users=(SELECT count(*) FROM sentryx_issue_users u WHERE u.issue_id=i.id);
CREATE INDEX IF NOT EXISTS sentryx_issues_project_users_idx
  ON sentryx_issues (project_id, users DESC, id DESC);
