-- forward-refusal-guard: refusals of the tools that send or forward to someone, per run. A fixable one (a missing
-- brief, an invalid or too-long field) holds the run's done/handoff/submit/wait exit until a later call with the same
-- request_id succeeds (cleared_at); a decision (question limit, conflict, authority) sends an exit after it to the
-- agent's manager.
CREATE TABLE run_refusals (
  company_id text NOT NULL,
  id bigserial,
  run_id text NOT NULL,
  tool text NOT NULL,
  request_id text NOT NULL DEFAULT '',
  status integer NOT NULL,
  reason text NOT NULL DEFAULT '',
  detail text NOT NULL DEFAULT '',
  fixable bigint NOT NULL,
  created_at bigint NOT NULL,
  cleared_at bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (company_id, id)
);
CREATE INDEX run_refusals_open ON run_refusals(company_id, run_id) WHERE cleared_at = 0;
