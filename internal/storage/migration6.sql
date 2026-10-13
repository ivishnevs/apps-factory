CREATE TABLE artifact_versions (
 project_id TEXT NOT NULL REFERENCES projects(id), id TEXT NOT NULL, version TEXT NOT NULL,
 uri TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(project_id,id,version)
);
ALTER TABLE approvals ADD COLUMN kind TEXT NOT NULL DEFAULT 'action';
ALTER TABLE approvals ADD COLUMN artifact_id TEXT;
ALTER TABLE approvals ADD COLUMN artifact_version TEXT;
CREATE UNIQUE INDEX active_artifact_approval ON approvals(project_id,artifact_id,artifact_version)
 WHERE kind='artifact' AND status IN ('PENDING','APPROVED');
CREATE INDEX artifact_approval_history ON approvals(project_id,artifact_id,artifact_version);
PRAGMA user_version=6;
