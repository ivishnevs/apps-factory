## 1. Scope

- [x] 1.1 `communication.can_assign_tasks`: `none | direct_reports | subtree | [agents]`, defaults by whether the agent
      has reports; the boolean replaced outright; validation (unknown or other-team agent: error; `direct_reports`
      without reports: warning); tests.
- [x] 1.2 Core refuses create, assign and reassign to an agent outside the caller's scope, naming the agent to use
      (the assignee's chain member that is in scope); owner and admins unaffected; tests.

## 2. Roster

- [x] 2.1 Instructions list only the agents in scope, each with its subtree's coverage; tests.

## 3. UI and docs

- [x] 3.1 Web team page shows whom each agent assigns work to (`assigns_to`); the scope is edited in the agent's YAML (Advanced), the TUI has no agent permission view to extend.
- [x] 3.2 `docs/agent-configuration.md`, `docs/factory-mcp.md`.

## 4. Verify

- [x] 4.1 Replay 2026-11-11 with fakes:
      - the CEO's `factory_task_create` for developer-lite is refused with "give the task to product-manager";
      - the CEO's roster lists only its direct reports;
      - the PM can still assign to developer-lite.
