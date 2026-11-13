Status: approved by the owner 2026-11-13. The owner chose the assignee's manager (`reports_to`) as the default
reviewer and asked for a team-level default reviewer (e.g. a QA agent).
Sections 1, 3 and 4 implemented 2026-11-13. Implementation decisions: an approval "waits" while it is `PENDING` and
unexpired, also when a delegate holds it (its decision moves the task the same way); the task's versions are those
whose `task_id` is the task (registered from its runs or an exit). `awaiting_approval` is a park kind (reason
`parked:awaiting_approval`, migration 0044 widens the kind check), not a task status: set by an approved verdict while
a hold exists, or by `factory_exit_wait approval_id`; authority input does not override it. The decision moves
submitted work only (in review, or reviewed); a task before its first submit just gets the decision, and a live run
is not moved (it sees the decision as authority input). Changes requested: READY to the implementer with the request
as `review_feedback`, its PR approvals dropped. Rejected: `abandon_branch` set, PR approvals dropped. A superseded or
expired approval releases a parked task (an expired one does not hold: the version is not approved and the agent
asks again). An approved verdict is refused while the latest approval of a submitted version asked for changes or
rejected it (`artifact_decided_against`). The merge guard is a new builtin mandatory policy `task-merge` with a
builtin-only effect `check` on `gh pr merge*`, `gh * pr merge*`, `git push*`, `git * push*`: the PATH shim asks Core,
which parses `gh … pr merge` and a push whose refspec targets the default branch (project `default_branch`, else the
task's last exit facts, else `main`/`master`; `--all`, `--mirror`); a plain `git push`, merges through `gh api` and
nodes older than this release are not checked (done is still refused). The binding reviewer is resolved in storage
(Core passes the default): a named reviewer passes when it is the task's or a path rule's reviewer for the PR's
files; a task without one (or whose reviewer is the submitting agent) records the default. The team setting is
`spec.review.reviewer` (an agent of the team or `role:<role>`), edited with `reviewer` on `PUT /catalog/teams/{name}`
and on the team page. Changing `review_by` is allowed to people, the creator and the managers of the implementer
(in review) or assignee. A reviewer's handoff without a verdict hands the review on (the task stays IN_REVIEW). The
verdict field is `artifact_checks: [{id, version, checked, note}]` (`id@version` accepted), stored as the task's
`review_checks` (latest approved verdict; cleared on a new submit, changes or rejection); a submission without
artifacts needs none, and people's own accept records none. Telegram's `/status` lists awaiting-approval tasks.

## Why

Seen on 2026-11-13 in tvorog, project stashy, task "prototype v3 — kangaroo in the new style + icon options".

- **Merged before people decided.** product-designer uploaded the prototype `stashy-home-design@v3` and requested
  approval; the approval went to the owner. The product manager reviewed PR #8, the designer merged it into the
  default branch and exited `done` while the owner had not decided. Had the owner rejected the design, unapproved files
  would already be on the default branch, the rejection would land on a closed task, and anyone building from the
  default branch could start from a design nobody approved. The task page showed `done` next to "Waiting for your
  approval".
- **Prototypes do not open in the web.** HTML artifacts render in an iframe with `sandbox=""` and are served with
  `Content-Security-Policy: sandbox`, so their scripts never run: a prototype drawn by JavaScript shows an empty phone
  frame, in the preview and in a new tab.
- **The implementer picked its reviewer.** The task's reviewer was `ceo`, suggested by Core because the review policy
  falls back to the team's lead and ignores the hierarchy (the designer reports to the product manager). On submit the
  designer named the product manager as reviewer, and Core took the submit's reviewer over the task's
  (`internal/core/exits.go`, `internal/storage/exits.go`); the task page kept showing `ceo`.
- **Approved without looking.** Three times that day the product manager approved design work while writing "did not
  run the prototype, accepted the rest from the designer's description".

## What Changes

### 1. A task whose artifacts wait for people does not finish

- While an artifact version the task produced (registered from its runs) has an approval waiting for people (pending,
  or escalated to people), Core refuses `done` and refuses merging the task's pull request. The merge is guarded at
  the command (`gh pr merge`, a push to the default branch) by Core's command guard, not only at the exit.
- After review the task waits for the decision as `awaiting_approval`, shown as such on every surface.
- The decision moves the task: **approved / chosen** — the task wakes, the agent merges and exits `done`; **changes
  requested** — the task goes back to its agent on the same branch with the requested changes and is submitted again;
  **rejected** — the task wakes, the agent closes the pull request unmerged and the task ends without the branch being
  merged (Core accepts the end as for `abandon_branch`).
- Decisions by a delegate move the task the same way (holder rule, people-topics D18).
- Approvals of other tasks' artifacts and tool-call (command) approvals do not hold the task.

### 2. Interactive artifacts open in the web

- HTML artifacts render with scripts allowed in an opaque origin: iframe `sandbox="allow-scripts"` (no
  `allow-same-origin`, forms, popups or top navigation) and raw content served with
  `Content-Security-Policy: sandbox allow-scripts`. The page runs but cannot read the person's session, cookies or the
  Factory API. Applies to the material viewer, the inbox preview and opening in a new tab.

### 3. The task's reviewer is binding and defaults to the manager

- **Default reviewer** (Core's suggestion and fill): the reviewer set in the assignee's review policy; else the
  team's default reviewer (a new team setting, e.g. a QA agent); else the assignee's manager (`reports_to`); else
  the team's lead; else the owner. Never the assignee.
- **Binding:** a submit goes to the task's reviewer. A submit naming a different reviewer is refused with the task's
  reviewer named. Changing a task's reviewer is an authority action (the assignee's manager up the hierarchy, the
  task's creator, or people), like reassigning. A task without a reviewer takes the default at submit. Path-based
  review policies (a reviewer required for certain files) keep working and are checked in addition.
- Every surface shows the reviewer who actually reviews.

### 4. "Approved" covers every submitted artifact

- An `approved` verdict lists each artifact of the submission as **checked** (with what was checked) or **not
  checked** (with why). Core refuses `approved` when an artifact is missing from the list or marked not checked; the
  reviewer checks it, asks for changes, or hands the review to someone who can check it.
- The checks are shown to people on the task with the reviewer's name.
- Core cannot verify that an agent looked; it makes the claim explicit, recorded and attributable.

## Impact

- Specs: `run-exits`, `artifact-approvals`, `task-engine` (`awaiting_approval`), `review-policy` (default reviewer,
  binding reviewer, verdict coverage), `owner-web` (interactive preview, task status and reviewer).
- Code: `internal/storage` (exit checks, approval → task transition, submit reviewer), `internal/core` (review gate
  suggestion, exit hints, command guard for merge), `internal/config` (reviewer by hierarchy), `internal/web` (CSP),
  web viewer and inbox preview, TUI and task page.
- Data: no migration expected.
