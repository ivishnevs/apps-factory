-- People topics (people-topics): everything that reaches people passes one gate, which puts it into a topic and
-- checks its brief. A topic groups the items people decide about one subject (an artifact's version line, a task, an
-- agent's escalations of one reason); items are references to the existing approvals, questions, escalations,
-- decisions and reviews, whose mechanics do not change.
CREATE TABLE people_topics (
  company_id text NOT NULL,
  id text NOT NULL,
  project_id text NOT NULL,
  key text NOT NULL,
  status text NOT NULL,
  brief jsonb NOT NULL DEFAULT '{}',
  needs_brief boolean NOT NULL DEFAULT false,
  brought_by text NOT NULL DEFAULT '',
  conversation_id text NOT NULL DEFAULT '',
  created_at bigint NOT NULL,
  updated_at bigint NOT NULL,
  settled_at bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (company_id, id)
);
CREATE INDEX people_topics_key ON people_topics(company_id, project_id, key, status);
CREATE TABLE people_topic_items (
  company_id text NOT NULL,
  topic_id text NOT NULL,
  kind text NOT NULL,
  ref_id text NOT NULL,
  lineage text NOT NULL DEFAULT '',
  version text NOT NULL DEFAULT '',
  note jsonb NOT NULL DEFAULT '{}',
  brought_by text NOT NULL DEFAULT '',
  state text NOT NULL,
  added_at bigint NOT NULL,
  seq bigserial,
  PRIMARY KEY (company_id, topic_id, kind, ref_id)
);
CREATE INDEX people_topic_items_ref ON people_topic_items(company_id, kind, ref_id);
CREATE INDEX people_topic_items_line ON people_topic_items(company_id, lineage) WHERE lineage <> '';
ALTER TABLE messages ADD COLUMN topic_id text NOT NULL DEFAULT '';
CREATE INDEX messages_topic ON messages(company_id, topic_id) WHERE topic_id <> '';
-- The language people read briefs in (people-topics D15): agents write briefs in it, Core's templates use it.
ALTER TABLE companies ADD COLUMN people_language text NOT NULL DEFAULT 'en';
