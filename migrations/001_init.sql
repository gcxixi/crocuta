CREATE TABLE IF NOT EXISTS sentryx_ingest_jobs (
  id BIGSERIAL PRIMARY KEY,
  project_id TEXT NOT NULL,
  received_at TIMESTAMPTZ NOT NULL,
  payload BYTEA NOT NULL,
  checksum TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'ready',
  attempts INTEGER NOT NULL DEFAULT 0,
  lease_until TIMESTAMPTZ,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  error_code TEXT,
  error_detail TEXT,
  completed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS sentryx_ingest_jobs_ready_idx ON sentryx_ingest_jobs (state, available_at, id);

CREATE TABLE IF NOT EXISTS sentryx_issues (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  title TEXT NOT NULL,
  level TEXT,
  count BIGINT NOT NULL DEFAULT 0,
  first_seen TIMESTAMPTZ NOT NULL,
  last_seen TIMESTAMPTZ NOT NULL,
  latest_event_id TEXT NOT NULL,
  grouping_version INTEGER NOT NULL,
  group_hash TEXT NOT NULL,
  UNIQUE (project_id, grouping_version, group_hash)
);
CREATE INDEX IF NOT EXISTS sentryx_issues_project_last_seen_idx ON sentryx_issues (project_id, last_seen DESC);

CREATE TABLE IF NOT EXISTS sentryx_events (
  id BIGSERIAL PRIMARY KEY,
  project_id TEXT NOT NULL,
  event_id TEXT NOT NULL,
  issue_id TEXT REFERENCES sentryx_issues(id),
  occurred_at TIMESTAMPTZ NOT NULL,
  received_at TIMESTAMPTZ NOT NULL,
  canonical_json JSONB NOT NULL,
  UNIQUE (project_id, event_id)
);
CREATE INDEX IF NOT EXISTS sentryx_events_issue_received_idx ON sentryx_events (project_id, issue_id, received_at DESC);
