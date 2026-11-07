# Unified policies

One catalog in configs/policies/*.yaml contains access, escalation, reporting and recovery rules.
Use apiVersion factory/v1, kind Policy, metadata.name and spec:

```yaml
spec:
  extends: optional-parent
  mandatory: true
  rules:
    - name: protect-release
      type: access
      operations: [factory.release.publish]
      effect: approval
      instruction: Ask the owner before publishing a concrete app version.
    - name: product-question
      type: escalation
      roles: [developer]
      target: manager
      instruction: Escalate unresolved product decisions to the manager.
    - name: report-dependencies
      type: reporting
      instruction: Report dependency installation, its purpose and result.
    - name: inspect-publishing
      type: recovery
      mode: inspect
      roles: [publisher]
      instruction: Publishing runs are inspected, not retried.
```

Agents attach policies by name. All mandatory policies also apply automatically.
Parent rules are additive and cannot be overridden with the same rule name; duplicate names in a
resolved policy fail validation. A mandatory parent remains mandatory when a child says false.
Identical inherited rules selected through multiple policies are rendered once.
Optional roles/agents filters match the agent role/name; both filters must match when both exist.

Access decisions combine tool filters and every applicable rule: deny > approval > allow.
An allow rule cannot override a denylist or grant a tool absent from an allowlist. For escalation,
target is self/manager/owner; instructions govern the trusted agent. Reporting instructions describe
significant events; agents persist these through factory_report_create. Reporting is part of the
same policy system, independent of access approval and escalation.
A recovery rule (`mode: inspect`, the only mode; no operations, effect or target) marks the agent's runs as
acting outside the workspace: an unclear end parks the task for inspection instead of an automatic retry
(reliable-recovery D4, [task lifecycle](task-lifecycle.md#failure-retry-and-cancellation)). The run's config
snapshot carries it, so a later policy change does not change how a running run recovers. `factory agents
authorize NAME OPERATION` prints the agent's effective `recovery` mode (`inspect` or `retry`).
Behavioral instructions with contradictory escalation/reporting rules are not automatically
arbitrated; configure consistent instructions. Access conflicts have the precedence above.

The reserved immutable `system` policy requires owner approval for project deletion, release
publication, production credentials, paid external purchases and factory configuration changes.
It cannot be replaced by YAML, removed from an agent or bypassed using unrestricted/never.
The protected operation names are factory.project.delete, factory.release.publish,
factory.secrets.access_production, external.purchase, factory.config.* and factory.decision.record.

## Team selector

Every rule takes `teams: [...]` next to `roles` and `agents` (company-teams D7): it then applies to the agents of
those teams only. A rule without `teams` applies to every team; mandatory policies still apply to everyone.

```yaml
    - name: saas-deploys
      type: access
      teams: [saas]
      operations: [factory.deploy.prod]
      effect: deny
      instruction: SaaS deploys through the release train.
```

## Command guards

Access rules may list shell `commands` instead of (or besides) `operations` (node-agent D6):

```yaml
    - name: simulator-taps
      type: access
      roles: [developer]
      commands: ["xcrun simctl io * tap*"]
      effect: approval
      instruction: Prefer UI tests by accessibility identifier; ask the owner for one-off taps.
```

A pattern matches the basename of argv[0] or the command line (that basename and the arguments
joined by single spaces); `*` matches any text including spaces and slashes, `?` one character.
The first word must be a literal command name (no glob, no path): it names the shim. Commands are
only valid on access rules and resolve per agent like operations (roles/agents filters, mandatory
policies, deny > approval > allow; no matching rule allows).

Reserved mandatory policies, defined in code like `system` (YAML cannot replace them):
- `host-ui` denies `cliclick`, `osascript`, `osacompile` and `xdotool`: use debug launch arguments,
  deep links or UI tests by accessibility identifier. Mobile agents get the concrete iOS/Android recipes from
  the shared [`mobile-app-testing` skill](skills.md#mobile-app-testing).
- `owner-cli` denies the `factory` owner CLI: use the Factory MCP tools.

The executor reads the rules captured in the run's agent snapshot and, when any deny/approval rule
names a command, creates a per-run directory first on the runtime PATH (`FACTORY_SHIM_DIR`) with
`rules.json` and one symlink per guarded command to `factory-shim`. The shim decides the exact argv
(argv[0] reduced to its basename):
- allow: exec the real binary, found on PATH without the shim directory (127 when missing);
- deny: print the rule's instruction, report to Core (best effort), exit 126;
- approval: `POST /agent/command` with the run token and exact argv; Core consumes one APPROVED
  single-use `command` grant and answers 200, otherwise the shim exits 126 and tells the agent to
  request `factory_owner_request_approval` with
  `call: {"name":"command","arguments":{"argv":["git","push","origin","main"]}}` and finish the turn.
  An approved command runs exactly once; a second run or changed arguments are refused.

Core side: `POST /agent/command` (run token; JSON answer `{effect, rule, instruction, approval_id}`, 403 deny,
423 approval without a grant, 200 allow) decides with the captured and the current agent and consumes grants;
see [approvals](approvals.md#command-guards).

This is a guardrail, not a sandbox: agents run as the same OS user and an absolute path bypasses it.

Codex (runtime-drivers D2) also decides commands inside the runtime. When the captured rules deny or gate a
command, the thread starts with `approvalPolicy: untrusted`, so `codex app-server` asks before every command
outside its known-safe read-only set (those still meet the PATH shims); without such rules it stays `never`. The
driver splits the requested command into simple commands (unwrapping `sh|bash|zsh -c|-lc SCRIPT`, splitting at
`;`, `&`, `|`, newlines and parentheses, dropping `VAR=value` prefixes) and decides each argv with the same rules.
Redirections are not arguments: `[n]>file`, `[n]>>file`, `[n]<file`, `&>file`, `&>>file`, `[n]>&m`, `[n]<&m`,
`>|file`, `<>file` and `<<<word` are dropped with their target, and the `&` in `2>&1` or `&>` separates nothing, so
`git push origin main 2>&1 | tail` asks for exactly `git push origin main`. What the lexer cannot follow is treated
like an unexpanded argument (a gated part is declined, never granted): a heredoc (`<<`, `<<-`) or process
substitution (`<(...)`, `>(...)`) makes its command and every later one in the line ambiguous, as does a
redirection without a target or a `{var}>` descriptor.
- deny: `decline`, reported to Core (best effort), and the rule's instruction steered into the turn
  (`turn/steer`; the approval answer carries only the decision);
- approval: `POST /agent/command` for the gated argv; `accept` only when Core answers 200 with a consumed grant,
  otherwise `decline` with the same approval hint as the shim. The consumed grant is handed to the shim
  (`$FACTORY_SHIM_DIR/grants/`, single use, 2 minutes) so the shimmed binary runs once without asking Core again.
  All parts are judged before Core is asked, since asking consumes a grant: a command with two or more gated parts,
  or a gated part with `$VAR`, `${VAR}`, `$(...)` or backquotes (not an exact argv), is declined without asking
  Core, telling the agent to run each gated command separately with literal arguments;
- allow: `accept`. File change requests and input to a running terminal are accepted (no rules cover them).

The line judgment lives in one place, `shim.Guard` (`internal/node/shim`), used by the Codex driver in process and
by Claude through `factory-shim --decide` (stdin `{"command": "<shell line>"}`, stdout `{"decision": "allow"|"deny",
"reason"?}`, with `FACTORY_SHIM_DIR`, `FACTORY_URL`, `FACTORY_RUN_TOKEN` from its environment; any failure is a
deny). Claude (runtime-drivers D2, [claude-runtime](claude-runtime.md#per-run-shim-srcshimts)) runs it from a
`PreToolUse` hook for every `Bash` and `Monitor` command: deny blocks the tool call with the reason, approval passes
only with a consumed grant handed to the PATH shim, allow passes.

`factory agents authorize AGENT OPERATION` previews the decision without executing an operation.
The API exposes POST /agents/{agent}/authorize with {"operation":"..."}. This is an owner-level
inspection endpoint, not an agent capability grant. Actual skill reads/lists/searches enforce
the evaluator: deny -> 403, approval -> 423. No boolean approval bypass is accepted. Future tools
must use the same evaluator and bind approval to concrete parameters and artifact versions.

Full host access means prompt rules and Factory tool filtering are not strict OS isolation.
Runtimes get only lease-bound run credentials; nodes sign their requests with their own keys; the shared Core token identifies the
owner and trusted service callers.

## Wait and reporting rules

`type: wait` rules set `min_wait_interval` (15m..30m, default 15m) and `max_waits` (default 12) for the agents
they apply to (task-lifecycle.md, Waits). `type: reporting` rules may name `exits` (e.g. `[submit, done]`) and
`report_kinds`: a report is then expected from the run on those exits; a missing one never refuses the exit but
writes `report.missing` for the manager and a reminder in the agent's next brief
(reports-decisions.md, Event feed and costs).

## Review rules

A `review` rule (company-self-service D3) says, for the roles, agents or teams it covers, whether their work needs
review before it is done and who reviews it:

```yaml
    - name: code-review
      type: review
      roles: [developer]
      required: true          # done of a repository task refuses review_required without an approved verdict
      reviewer: reviewer      # an agent, or role:<role>; the default reviewer of factory_exit_submit
      instruction: Code changes are reviewed before they are merged.
    - name: requirements-review
      type: review
      roles: [developer]
      paths:                  # files a pull request changes need that reviewer's approval as well
        - match: openspec/specs/**
          reviewer: product-owner
      instruction: Requirement changes need the product owner's approval.
```

Core reads the rules from the run's captured configuration. `required`: `factory_exit_done` of a task in a
project with a repository and something to merge (a pull request, or commits ahead of the default branch) is
refused (`409 review_required`) unless an approved review verdict is bound to its pull request. `paths`
(`**` spans directories): when the pull request changes a matching file (the adapter reports the PR's files),
done also needs an approval by that path's reviewer. A reviewer is resolved in the agent's team, which is the
project's (company-teams D5): `role:qa` is the `qa` agent of that team, a named reviewer must be another agent of
that team. Without review rules the agent's prompt decides whether work goes to review.

### The task's reviewer (review-coverage)

Every task outside the backlog names its reviewer in `review_by`: another agent, `owner`, or `member:USER`.
Core refuses to create a task ready, to release it (`tasks ready`, Run now, Retry) or to clear `review_by` outside
the backlog without one: `400 Reviewer is required: set review_by …; the review policy suggests review_by "NAME"`.
The suggestion (`GET /tasks/review-suggestion?assignee=AGENT`, which prefills the web forms) is the agent's review
rule reviewer, else its team's lead, else the owner. A submit that names no reviewer goes to the task's agent
reviewer; a person as reviewer sends the review to people (the task shows "Waiting for your review", the Inbox lists
it under "Reviews for you" and Telegram tells the owner), and the policy's default reviewer is not applied.
Saving the catalog returns `warnings` for agents no review rule covers, with whom their tasks default to.
`POST /tasks/review-fill` (admins; `{"dry_run": true}` lists only) gives open tasks without a reviewer the
suggestion once.
