Status: proposed 2026-11-07 by the owner ("the manager must check open tasks, but not on every task of a big
decomposition; Factory adds deterministic checks that warn and never block"). Nothing implemented.

## Why

Seen on the owner's company (Gnome Arena) on 2026-11-07, three duplicate pairs in one day, all created by the producer:

| Duplicate | How it happened |
|---|---|
| 3D-P2i `ae279f64` vs 3D-K2i `ade31ac8` + 3D-O4i/W1i `a83575e3` | P2i was split into K2i and O4i/W1i; the original stayed READY. Both parts merged, then P2i ran again and could only add a docs PR (#170); its play review created new 3D work. |
| 3D-WQ2 `c9b076b8` vs 3D-WQ2 `d2a9b701` | A narrower check for the same items (world-models 7.1–7.3) was created; the old one was not cancelled. |
| `438cd999` vs `63947756` (player-profile-and-xp 3.2) | 05:44, owner conversation run: follow-up for PR #148. 13:26, a review run of PR #166 (archive of the change): "Nothing was tracking them, so I created a backlog task". The second run had no memory of the first and did not search. |

Why the manager does not check today:

1. **Checking is expensive.** `factory_task_list` returns every task of the project with full descriptions (285 on
   the game project), filtered only by status. One check costs a large part of a run's context, so agents skip it.
2. **Titles do not say what work a task covers.** The follow-up was titled "M5b-P3 follow-up …", its duplicate
   "player-profile-and-xp 3.2 remainder …": a search by change name or item number finds only one of them.
3. **Replacing and splitting leave no trace.** Nothing links a part to the task it was split from, or a new check to
   the one it replaces, so the original stays open.
4. **Core does not compare.** Task creation dedupes only replays of the same `request_id`.

Measured on the 285 tasks of the game project (scratch analysis, 2026-11-07):

- **Text similarity is not a usable warning.** The real duplicates have title token similarity 0.16–0.43 (Jaccard,
  stop words removed), while 65 unrelated pairs of the same assignee score ≥ 0.4 (verification tasks of different
  changes, the three class-talents specs). Description similarity is worse (0.13–0.31 for the duplicates).
- **Dependency overlap is noisy.** Siblings of one decomposition share dependencies by design (all M6 art tasks
  depend on the same brief): 44 pairs of the same assignee have equal or overlapping dependencies, and none of the 4
  duplicate pairs has equal dependency sets.
- **Same change + same assignee** catches 3 of the 4 pairs but also 76 legitimate pairs: useful as context, not as a
  warning.
- **An explicit reference to the same work item is precise.** `438cd999`/`63947756` both name "player-profile-and-xp
  3.2" and PR #148; the world-models pairs would match on items 4.x and 7.1–7.3 had the tasks recorded them. Today
  only `d2a9b701` names its items in text, and only 101 of 285 tasks set `openspec_change`.

So the precise, deterministic signal is "two open tasks cover the same work item", and it needs the item to be
recorded, which is cheap for the manager at creation time. Semantic similarity (embeddings or an LLM judge) could
add recall later, but it is not deterministic, costs a model call per creation and was not needed for any of the
three cases.

## What Changes

- **Work items on tasks (`covers`).** A task may list the work it covers as stable keys: an OpenSpec item
  `<change>#<n.m>` or group `<change>#<n>`, a pull request `pr#<number>`, a review finding or request
  `msg:<id8>`. Core normalises them and, when the field is empty, infers keys deterministically from the title and
  description (`<known change> <n.m>`, `tasks.md <n.m>` with `openspec_change`, `#<number>` for PRs); inferred keys
  are marked as such. `openspec_change` is inferred the same way when exactly one known change is named.
- **Explicit replacement (`replaces`).** `factory_task_create` and `factory_task_update` accept `replaces: [ids]`:
  for a split or a narrower re-plan. Core cancels each replaced open task in the same transaction when the actor may
  cancel it (created it or has authority) and records the link both ways; otherwise the new task is created and the
  answer warns that the original is still open.
- **Duplicate warnings on create and update, never a refusal.** After a task is created (or its title, covers,
  change or assignee changes), Core compares it with the open tasks of the project (BACKLOG, READY, IN_PROGRESS,
  IN_REVIEW, BLOCKED) and returns `warnings` with the task, each naming the other task (id, title, status, assignee)
  and the reasons:
  - `same_work_item`: a shared `covers` key (strong);
  - `same_title`: equal normalised titles with the same assignee (strong);
  - `original_open`: the new task has a `parent_task_id` or a `replaces` entry that is still open (strong);
  - `same_change_open`: other open tasks of the same assignee and change, as a compact context list (at most 10),
    not as a duplicate claim.
  Tasks created by the same run or under the same parent are not compared with each other, so a decomposition of
  many tasks does not warn about its own siblings.
- **The manager decides.** The creating agent sees the warnings in the tool answer with a hint: cancel the new task,
  update the existing one, or keep both and say why (`factory_task_update` with `not_duplicate_of`). A warning
  nobody answers stays on both tasks as "possible duplicate" in the board and in the manager's feed with attention,
  until one task is closed or the pair is marked not duplicate.
- **Cheap search, once per batch.** `factory_task_list` gets a compact form (id, title, status, assignee,
  `openspec_change`, `covers`, dependencies; description only on request) and filters `open`, `assignee`,
  `openspec_change`, `covers` and `query` (title words). One call before a decomposition replaces a per-task check.
- **Manager instructions.** The default manager prompt and the tool descriptions say: one compact list before a
  batch, set `covers` and `openspec_change` on follow-ups and carry-overs, use `replaces` when splitting or
  re-planning, answer duplicate warnings.

Not in scope: blocking creation, merging tasks automatically, semantic (model-based) similarity.

## Impact

Task model and storage (`covers`, `replaces` links, `not_duplicate_of`), task create/update handlers (inference,
comparison, warnings in the answer), `GET /tasks` filters and compact form, Factory MCP tool schemas and
descriptions, feed attention items, board badge in web and TUI, default manager prompt. Specs: `task-engine`,
`factory-mcp`. Related: `dependency-wait-escalations` (cancelled originals no longer leave stalled dependents when
`replaces` moves the dependency edges), `artifact-duplicates` (same idea for artifact versions).

## Interim (2026-11-07, owner's company)

- Duplicate `63947756` cancelled into `438cd999` (title now starts with "player-profile-and-xp 3.2", acceptance
  merged).
- Producer prompt (catalog v50): "No duplicate tasks": one `factory_task_list` per batch, update instead of
  creating, cancel the original when splitting or replacing, name follow-ups after change and item and set
  `openspec_change`. Its goal line now says prototype 2.0 with the current graphics.
