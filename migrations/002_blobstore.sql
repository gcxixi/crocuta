-- Artifact bytes may live in an external BlobStore. Keep source_map nullable
-- during the rolling migration so old workers can continue reading BYTEA.
ALTER TABLE sentryx_artifacts
  ADD COLUMN IF NOT EXISTS blob_key TEXT;
ALTER TABLE sentryx_artifacts
  ALTER COLUMN source_map DROP NOT NULL;
CREATE INDEX IF NOT EXISTS sentryx_artifacts_blob_key_idx
  ON sentryx_artifacts (blob_key)
  WHERE blob_key IS NOT NULL;
