-- The ledger's model of a run is the model that processed the most tokens, cache reads and writes included.
-- Counting only input+output named Claude Code's auxiliary model (a few uncached calls) instead of the run's
-- main model, whose prompt is mostly cache reads.
CREATE OR REPLACE VIEW run_cost_source AS
SELECT r.company_id AS company_id, r.id AS run_id, coalesce(r.task_id,'') AS task_id, coalesce(r.spec->>'project','') AS project_id,
 r.agent AS agent, coalesce(r.spec->>'provider','') AS provider,
 coalesce((SELECT u->>'model' FROM jsonb_array_elements(usage_list(r.usage)) u
  ORDER BY coalesce((u->>'input')::numeric,0)+coalesce((u->>'output')::numeric,0)
   +coalesce((u->>'cache_read')::numeric,0)+coalesce((u->>'cache_write')::numeric,0) DESC LIMIT 1),
  coalesce(r.spec#>>'{runtime_options,model}','')) AS model,
 r.outcome AS outcome,
 coalesce((SELECT sum(coalesce((u->>'input')::numeric,0)) FROM jsonb_array_elements(usage_list(r.usage)) u),0)::bigint AS input_tokens,
 coalesce((SELECT sum(coalesce((u->>'output')::numeric,0)) FROM jsonb_array_elements(usage_list(r.usage)) u),0)::bigint AS output_tokens,
 coalesce((SELECT sum(coalesce((u->>'cache_read')::numeric,0)) FROM jsonb_array_elements(usage_list(r.usage)) u),0)::bigint AS cache_read_tokens,
 coalesce((SELECT sum(coalesce((u->>'cache_write')::numeric,0)) FROM jsonb_array_elements(usage_list(r.usage)) u),0)::bigint AS cache_write_tokens,
 coalesce(r.finished_at,0) AS finished_at
FROM runs r;

-- Relabel the rows already recorded (the cost does not depend on the label).
UPDATE run_costs c SET model=s.model FROM run_cost_source s WHERE s.run_id=c.run_id AND c.model<>s.model;
