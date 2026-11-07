## 1. Model and inference

- [ ] 1.1 `covers` (keys with an inferred flag), `replaces` / `replaced_by` links and `not_duplicate_of` pairs in the
      task model and Postgres storage; migrations; key normalisation (`<change>#<n.m>`, `<change>#<n>`, `pr#<n>`,
      `msg:<id8>`).
- [ ] 1.2 Deterministic inference from title and description: change names known from the project's tasks and
      checkout, `tasks.md <n.m>` with `openspec_change`, `#<n>` PRs; `openspec_change` when exactly one change is
      named; table tests with the three cases of 2026-11-07.

## 2. Replacement and warnings

- [ ] 2.1 `replaces` on create and update: cancel in the same transaction when the actor may cancel, link both ways,
      move dependency edges of the replaced task to the replacing tasks; `original_open` otherwise; tests.
- [ ] 2.2 Comparison after create/update against open tasks: `same_work_item`, `same_title` (normalised: lowercase,
      milestone prefixes such as `3D-WQ2:` and ids removed), `original_open`, `same_change_open` (≤ 10); sibling
      exemption by origin run and parent; `warnings` in the answer; never a refusal; tests.
- [ ] 2.3 Persist unanswered strong warnings; show them on both tasks (web board badge, TUI marker) and as attention
      items in the creator's manager feed; clear on close or `not_duplicate_of`.

## 3. Tools and instructions

- [ ] 3.1 `GET /tasks` compact form and filters (`open`, `assignee`, `openspec_change`, `covers`, `query`, `full`);
      `factory_task_list` schema; tests.
- [ ] 3.2 `factory_task_create` / `factory_task_update` schemas and descriptions (`covers`, `replaces`,
      `not_duplicate_of`, warnings hint); default manager prompt: one compact list per batch, covers on follow-ups,
      replaces when splitting.

## 4. Verify

- [ ] 4.1 Replay on a sandbox company: the three duplicate pairs of 2026-11-07 warn (or are prevented by
      `replaces`); a decomposition of 10 tasks of one change raises no warning; listing open tasks of one change
      costs under 5 % of the full list's size on the game project's data.
