-- artifact-choice-approvals: the artifact versions a choice approval offers. A version's approval is found whether it
-- was asked alone (approvals.artifact_id/artifact_version) or as an option of a choice; once an option is chosen,
-- the approval's artifact_id/artifact_version are the chosen version.
CREATE TABLE approval_options (
  company_id text NOT NULL,
  approval_id text NOT NULL,
  project_id text NOT NULL,
  position int NOT NULL,
  artifact_id text NOT NULL,
  artifact_version text NOT NULL,
  PRIMARY KEY (company_id, approval_id, position),
  FOREIGN KEY (company_id, approval_id) REFERENCES approvals(company_id, id)
);
CREATE INDEX approval_options_version ON approval_options(company_id, project_id, artifact_id, artifact_version);
