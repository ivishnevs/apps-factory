-- people-attachments: images a person uploads before naming them in a message or a comment. An upload not named
-- within a day is deleted (D5); one named by a message or comment lives as long as it (D6).
CREATE TABLE uploads (
  company_id text NOT NULL REFERENCES companies(id),
  sha256 text NOT NULL,
  name text NOT NULL,
  size bigint NOT NULL,
  actor text NOT NULL DEFAULT '',
  created_at bigint NOT NULL,
  PRIMARY KEY (company_id, sha256)
);
CREATE INDEX uploads_created ON uploads(company_id, created_at);
-- A comment's images (people-attachments), as messaging.Attachment.
ALTER TABLE task_comments ADD COLUMN attachments jsonb NOT NULL DEFAULT '[]';
