-- Artifact retention (artifact-retention): a superseded artifact version older than its project's retention is
-- archived. Its row stays (who, when, digest, size, task links, approvals); archived_at and archived_reason say
-- when and why, and its content is deleted once no live version, run log, attachment or skill uses the digest.
-- The project's artifact_retention_days lives in its body (0 keeps versions forever).
ALTER TABLE artifact_versions ADD COLUMN archived_at bigint NOT NULL DEFAULT 0;
ALTER TABLE artifact_versions ADD COLUMN archived_reason text NOT NULL DEFAULT '';
