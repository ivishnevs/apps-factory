Status: proposed and implemented 2026-11-12 by the owner's request (delegation follows the reporting tree in Core,
not in prompts).

## Why

Seen on 2026-11-11 in tvorog. The CEO created two tasks for developer-lite directly (`85a90314` merge the RouteSheet
patch, `a8ad9e45` the sheet scroll bug), bypassing the Product Manager, although its prompt says "don't create
Developer, Designer or Reviewer tasks yourself". The system invited it:

- `can_assign_tasks` is a yes/no switch. Core checks only the switch (`internal/core/tasks.go`), never the assignee,
  so an agent that may assign may assign to anyone of its team.
- The instructions of every agent that may create or assign tasks list the whole team (`internal/config/config.go`,
  "Your team (assign work by what each agent does and needs)…"), with each agent's capabilities. The CEO saw
  developer-lite as "the cheap agent for small iOS changes" and picked it.

So routing lives in prompts, and prompts lose to the roster and to "prefer cheaper agents". The owner wants routing in
the system, so prompts carry only each role's craft and the company's facts.

## What Changes

- **`can_assign_tasks` is a scope, not a switch.**
  ```yaml
  communication:
    can_assign_tasks: direct_reports   # default for an agent with reports
    # none: assigns to no one (default for agents without reports)
    # subtree: anyone below it in the reporting tree
    # [product-manager, app-release]: an explicit list
  ```
  `can_create_tasks` stays a switch. A task an agent creates without an assignee, or for itself, can be assigned only
  within its scope. `true` and `false` stay valid as short forms of `direct_reports` and `none`, so stored catalogs keep
  loading; the repository's configs and presets drop the key and take the default from the tree.
- **Core checks the assignee.** Creating, assigning or reassigning a task to an agent outside the scope is refused
  with the agent to use instead: "developer-lite reports to product-manager: give the task to product-manager". The
  owner and people with the owner or admin role still assign to anyone.
- **The roster follows the scope.** The instructions list only the agents the agent may assign to, each with what its
  own subtree covers ("product-manager: runs development; its team: developer, developer-lite, product-designer,
  reviewer"). An agent picks from what it may use, not from the whole team.
- **Unchanged:**
  - questions and escalations up the chain;
  - a manager's authority over its whole subtree (`factory_task_direct`, pause, resume, cancel, reprioritize): the
    CEO can stop any task below it, but hands out new work through its reports;
  - conversations and messaging (`can_message`).
- **Catalog validation:**
  - an error for a listed agent outside the team or unknown;
  - a warning for `direct_reports` on an agent without reports.

For tvorog:
- CEO → product-manager, app-release, appstore-publisher, aso-specialist, launch-creative, product-researcher,
  reviews-support, ops;
- Product Manager → developer, developer-lite, product-designer, reviewer;
- everyone else → none, as today.

Cost: small changes go through the PM (one more run and some minutes); the PM then knows all work in its product,
which is its role.

## Impact

- Config: `communication.can_assign_tasks` scope, validation, the roster in instructions (`internal/config`).
- Core: assignee checks on create, assign and reassign, for MCP tools and agent HTTP calls; refusal with the right
  agent.
- Web and TUI: agent forms show the scope; the team page shows who may assign to whom.
- Prompts: routing ("give X to Y") leaves the role prompts once this ships; the tvorog CEO and PM keep one line
  "assign only to your direct reports" until then.
- Docs: `docs/agent-configuration.md` (hierarchy), `docs/factory-mcp.md`. Related: `owner-comments-handled`.
