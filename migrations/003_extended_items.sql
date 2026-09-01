CREATE TABLE IF NOT EXISTS sentryx_client_reports (
  id BIGSERIAL PRIMARY KEY,
  project_id TEXT NOT NULL,
  received_at TIMESTAMPTZ NOT NULL,
  report_timestamp TIMESTAMPTZ NOT NULL,
  discarded_events JSONB NOT NULL DEFAULT '[]'::jsonb
);
CREATE INDEX IF NOT EXISTS sentryx_client_reports_project_received_idx
  ON sentryx_client_reports (project_id, received_at DESC);

CREATE TABLE IF NOT EXISTS sentryx_signals (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  event_id TEXT,
  kind TEXT NOT NULL,
  received_at TIMESTAMPTZ NOT NULL,
  schema_version INTEGER NOT NULL DEFAULT 1,
  content_type TEXT,
  size BIGINT NOT NULL DEFAULT 0,
  blob_key TEXT,
  payload JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS sentryx_signals_project_kind_received_idx
  ON sentryx_signals (project_id, kind, received_at DESC);

CREATE TABLE IF NOT EXISTS sentryx_attachments (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  event_id TEXT,
  filename TEXT NOT NULL,
  content_type TEXT,
  size BIGINT NOT NULL,
  sha256 TEXT NOT NULL,
  blob_key TEXT,
  body BYTEA,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sentryx_attachments_project_event_idx
  ON sentryx_attachments (project_id, event_id, created_at DESC);

CREATE TABLE IF NOT EXISTS sentryx_releases (
  project_id TEXT NOT NULL,
  version TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, version)
);
