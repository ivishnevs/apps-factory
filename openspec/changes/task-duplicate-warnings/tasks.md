## 1. Model and inference

- [x] 1.1 `covers` (keys with an inferred flag), `replaces` / `replaced_by` links and `not_duplicate_of` pairs in the
      task model and Postgres storage; migrations; key normalisation (`<change>#<n.m>`, `<change>#<n>`, `pr#<n>`,
      `msg:<id8>`).
      Done: `covers`/`covers_inferred`, `replaces`/`replaced_by` in the task body; `task_duplicates` (migration 0033)
      holds open warnings and `not_duplicate` pairs, presented as `possible_duplicates` / `not_duplicate_of`;
      `tasks.NormalizeCover(s)` (internal/tasks/duplicates.go).
- [x] 1.2 Deterministic inference from title and description: change names known from the project's tasks and
      checkout, `tasks.md <n.m>` with `openspec_change`, `#<n>` PRs; `openspec_change` when exactly one change is
      named; table tests with the three cases of 2026-11-07.
      Done: `tasks.InferCovers` (lists and ranges such as `7.1–7.3`, `PR 148`), known changes = tasks' links and the
      `openspec_changes` reported by done exits; `TestInferCovers`, `TestDuplicateWarnings`.

## 2. Replacement and warnings

- [x] 2.1 `replaces` on create and update: cancel in the same transaction when the actor may cancel, link both ways,
      move dependency edges of the replaced task to the replacing tasks; `original_open` otherwise; tests.
      Done: cancels only BACKLOG/READY/BLOCKED/REWORK tasks without a live run or open children; later parts of an
      already replaced task are linked and get its dependents too; `TestReplaces`.
- [x] 2.2 Comparison after create/update against open tasks: `same_work_item`, `same_title` (normalised: lowercase,
      milestone prefixes such as `3D-WQ2:` and ids removed), `original_open`, `same_change_open` (≤ 10); sibling
      exemption by origin run and parent; `warnings` in the answer; never a refusal; tests.
- [x] 2.3 Persist unanswered strong warnings; show them on both tasks (web board badge, TUI marker) and as attention
      items in the creator's manager feed; clear on close or `not_duplicate_of`.
      Done: `task.possible_duplicate` attention feed row (agent = creator, quieted when the warning ends; skipped by
      Telegram); web card badge "duplicate?" and a "Possible duplicate" block with "Keep both"; TUI `≈dup` and
      `dup?` detail lines.

## 3. Tools and instructions

- [x] 3.1 `GET /tasks` compact form and filters (`open`, `assignee`, `openspec_change`, `covers`, `query`, `full`);
      `factory_task_list` schema; tests.
      Done: `GET /tasks?compact=1` plus the filters; `factory_task_list` is compact unless `full: true`; its
      `assignee` filter needs no assign permission; `TestTaskDuplicateWarningsMCP`.
- [x] 3.2 `factory_task_create` / `factory_task_update` schemas and descriptions (`covers`, `replaces`,
      `not_duplicate_of`, warnings hint); default manager prompt: one compact list per batch, covers on follow-ups,
      replaces when splitting.
      Done: tool schemas and descriptions; `duplicate_hint` in the answer; "No duplicate tasks" line in the
      instructions of every agent that creates or assigns tasks (internal/config/config.go).

## 4. Verify

- [x] 4.1 Replay on a sandbox company: the three duplicate pairs of 2026-11-07 warn (or are prevented by
      `replaces`); a decomposition of 10 tasks of one change raises no warning; listing open tasks of one change
      costs under 5 % of the full list's size on the game project's data.
      Done as automated tests with fakes (no prod data): `TestInferCovers` (the three pairs' texts),
      `TestDuplicateWarnings` (second follow-up warns, 10-task decomposition silent, same_title),
      `TestReplaces` (the P2i split cancels the original and moves its dependents), `TestTaskDuplicateWarningsMCP`
      (285 tasks shaped like the game project: one change's open tasks are under 5 % of the full list).
