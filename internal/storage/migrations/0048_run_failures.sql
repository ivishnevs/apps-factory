-- contracts-at-the-boundary §2: a run that failed before its agent started says what failed (failure_code) and who
-- can fix it (failure_fixer: implementer, node, owner, none); failure_head is the task's handed-over head (its
-- workspace's exit_sha) when it failed, so the same failure on the same head twice in a row is treated as deterministic.
ALTER TABLE runs ADD COLUMN failure_code text NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN failure_fixer text NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN failure_head text NOT NULL DEFAULT '';
