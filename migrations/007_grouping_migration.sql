CREATE TABLE IF NOT EXISTS sentryx_grouping_hashes (
  project_id TEXT NOT NULL,
  grouping_version INTEGER NOT NULL,
  group_hash TEXT NOT NULL,
  issue_id TEXT NOT NULL REFERENCES sentryx_issues(id) ON DELETE CASCADE,
  components JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, grouping_version, group_hash)
);
CREATE INDEX IF NOT EXISTS sentryx_grouping_hashes_issue_idx
  ON sentryx_grouping_hashes (issue_id);

-- Backfill the active mappings for databases upgraded from the original schema.
INSERT INTO sentryx_grouping_hashes (project_id, grouping_version, group_hash, issue_id)
SELECT project_id, grouping_version, group_hash, id
FROM sentryx_issues
ON CONFLICT (project_id, grouping_version, group_hash) DO NOTHING;
