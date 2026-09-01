ALTER TABLE sentryx_artifacts ADD COLUMN IF NOT EXISTS debug_id TEXT;
CREATE INDEX IF NOT EXISTS sentryx_artifacts_debug_id_idx
  ON sentryx_artifacts (project_id, debug_id)
  WHERE debug_id IS NOT NULL;
