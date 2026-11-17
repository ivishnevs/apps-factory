Status: accepted 2026-11-15 by the owner. Raised by the owner from the tvorog Unsealed onboarding the same day
("three questions about the same problem — isn't this a design error?").

## Why

On 2026-11-15 in tvorog the product manager's task c66e839e ("Audit and onboard Unsealed") ran into three gaps in a
row. None of them is specific to that company.

1. **The handed-over work was in the wrong place, and Core accepted it.** The node created the task worktree on
   `af/c66e839e…`. The agent ran `git checkout -b audit-onboard`, committed, pushed that branch, opened PR #19 from it
   and exited `submit`. The exit facts said `branch: audit-onboard`, and Core accepted them: `checkFacts` checks the
   secret scan, the clean tree and (for done) the merged PR, never the branch. The adapter does not know the task
   branch either, so its own `publish` pushes whatever is checked out.
2. **The failure went to someone who could not fix it.** The reviewer's run could not build its review copy (no
   `origin/af/<task>`) and ended `setup_failed: not reviewable … the implementer must push it`. Core counted that as
   an ordinary attempt of the reviewer and retried the reviewer at 1, 5 and 15 minutes, which repeated the same
   failure. It then escalated `retries_exhausted` to the owner with `retry | cancel`. Neither answer could clear the
   cause: retry reran the reviewer, cancel threw the audit away. Only the implementer could fix it, and no path sent
   the task back to them. (Since `aba6135` the review copy falls back to the handed-over SHA, so this exact review
   would start on current `main`. That fallback is for a task branch deleted after its merge, not for work on another
   branch: the review copy's pull request lookup (`7afd562`) and the restore of saved work on another node still go by
   the task branch name, so they would miss PR #19 and the work. And any other deterministic setup failure still loops
   the same way.)
3. **The contract the agent needed was hidden.** The agent called `factory_report_create` six times with kinds
   `audit`, `audit_report`, `project_audit`, `significant_action` and `other`. Every call failed with `unsupported
   report kind`. The five valid kinds exist only in `storage/journal.go`: the tool schema declares `kind` as a plain
   string, and the error lists nothing. The instruction came from Core itself: the generated audit brief
   (`core/projects.go` `auditBrief`) asks for "a report with options … `factory_report_create`, `needs_owner_action`",
   which fits no kind, and a report is not how the system asks the owner to choose. The agent then exited `done`
   without the report, and Core accepted it, because failed report calls are not held against exits.

The class: **Core checks the agent's work late, and sends what it finds to whoever happens to be assigned, instead of
checking at the boundary and sending the failure to whoever can fix it.** This follows the owner's principle that
what the system can enforce goes into the system, not into prompts. Today the only guard against branch switching is
a sentence in the developer prompt.

## What Changes

### 1. The task branch is part of the exit contract

- **The node tells an implementer run its task branch.** It sets `FACTORY_TASK_BRANCH` (`af/<task>`) for implementer
  runs, as it already sets `FACTORY_REVIEW_BRANCH` for review copies. The adapter records `task_branch` in the exit
  facts. Its `publish` always pushes `HEAD` to the task branch and opens or updates the pull request from it,
  whatever branch is checked out locally.
- **Core refuses code exits whose work is not on the task branch.** `submit`, `handoff` with code and `done` in a
  repository project are refused with `wrong_branch` when the facts' branch or the pull request's head branch is not
  the task branch. The hint says how to fix it in the same turn: push `HEAD` to the task branch, open the pull request
  from it, close a pull request opened from another branch, exit again. It is a fixable refusal, so it is recorded per
  run like the other fixable refusals.
- The review-copy fallback to the handed-over SHA (`aba6135`) stays as the safety net for merged and deleted branches.

### 2. A failure goes to whoever can fix it

- **Each setup failure says who fixes it.** The worker reports a setup failure as a code with a fixer instead of free
  text matched by prefix: `implementer` (the handed-over work is missing or unusable: not reviewable, saved work gone
  from the remote), `node` (the existing node faults), `owner` (local policy, missing credentials and secrets the
  owner provides) or `none` (transient: lock contention, network). The text stays for people and logs.
- **Core routes by the fixer, not by retry counting:**
  - `implementer`: the task goes back to its implementer as rework, with the cause as the brief ("your handed-over
    work could not be checked out for review: …, push it to the task branch and submit again"). The reviewer's
    attempt is not counted. If the implementer cannot be reached (removed from the team), Core escalates to the
    implementer's manager.
  - `node`: unchanged (node readiness, no attempt counted).
  - `owner`: one owner item naming what to provide, no retries first.
  - `none`: the existing bounded retry.
- **A deterministic failure is never retried as is.** The same failure code with the same handed-over head twice in a
  row is treated as deterministic and routed as above, even when the worker did not classify it.
- **Every escalation offers an answer that can clear its cause.** `retries_exhausted` on a task in review also offers
  `return` (back to the implementer with the cause). The `reassign` answer the run-recovery spec already promises
  ("Three failures": retry, reassign, cancel) is implemented. A test asserts that every escalation reason has at least
  one answer other than retry and cancel that changes who acts or what they act on.

### 3. Closed sets are declared where the agent reads them

- **One source for each closed set.** Every tool argument whose valid values are a closed set is declared with an
  `enum` in the tool schema, built from the same Go constant Core validates against. This covers report `kind`,
  `message_type`, question `type`, decision `status` (list and record), artifact `audience` and task-list `status`.
  The adapter's argument check enforces enums before the call leaves the node, so the agent gets one error listing
  every problem.
- **Every closed-set error lists the allowed values.** For example: `unsupported report kind "audit"; one of:
  dependency_installation, environment_change, plan_deviation, problem, stage_completed`. `task_list` refuses an
  unknown status instead of returning an empty list.
- **Failed reports are held against exits.** `factory_report_create` joins the tools whose failures are recorded as
  fixable refusals, so an agent cannot exit `done` past a report it failed to create. It must create the report or say
  in its exit why not.
- **A test ties schemas to validators.** For each closed-set argument, the schema's enum equals the validator's set.

### 4. Core's own instructions use only what Core accepts

- **The audit brief asks for a decision, not a report.** "What to do next" options for the owner are a decision
  proposal: `factory_decision_propose` with the recommendation as `value` and the other options as `alternatives`,
  each with a reason and a rough size. The summary goes into the pull request and a `stage_completed` report.
  `needs_owner_action` stays for reports that ask the owner to do something outside the system.
- **The brief states the branch rule** ("work on the task branch you were given; Core opens the pull request from
  it").
- **A test checks every Core-generated agent text** (onboarding and audit briefs, review briefs, hold hints, reminder
  texts): every `factory_*` tool it names exists, and every argument value it names is in that argument's set.
- The default library's `project-audit` skill, where shipped, is corrected the same way. A company's own copy is
  company data: the owner updates it with `factory catalog`.

Out of scope: where the owner sees what is asked of them, and how often. That is `owner-items-one-source`, which
removes the duplicate cards this incident produced.

## What was discussed

- **Accept any branch and review the pull request head instead.** Rejected: the task branch is what ties a task to
  its worktree, review copy, restore and merge. One branch per task keeps every other mechanism simple. Refusing at
  the exit costs the agent one turn while it still holds the context.
- **Retry less, escalate sooner.** Rejected: that only moves the dead end to the owner earlier. The fix is to route by
  who can fix it.
- **Prompt fixes** (developer prompt: "never checkout another branch"; PM prompt: "valid kinds are …"). Rejected by
  the owner's principle: prompts carry role craft; the system enforces its contracts.
- **No migration.** The project is unreleased. New exits and failures follow the new rules, and open tasks are not
  rewritten.

## Impact

- Node: `FACTORY_TASK_BRANCH`, adapter facts and `publish`, worker setup-failure codes.
- Core: exit refusal `wrong_branch`, failure routing in `recoverTask`, escalation answers, closed-set constants shared
  with the tool schemas, `report_create` in refusal tools, audit brief.
- Specs: run-exits, run-recovery, factory-mcp, project-creation, task-worktrees.
- Docs: `docs/explicit-exits-contract.md` (facts gain `task_branch`; branch rule).
