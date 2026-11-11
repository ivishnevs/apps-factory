## 1. Scope

- [ ] 1.1 `communication.can_assign_tasks`: `none | direct_reports | subtree | [agents]`, defaults by whether the agent
      has reports; the boolean replaced outright; validation (unknown or other-team agent: error; `direct_reports`
      without reports: warning); tests.
- [ ] 1.2 Core refuses create, assign and reassign to an agent outside the caller's scope, naming the agent to use
      (the assignee's chain member that is in scope); owner and admins unaffected; tests.

## 2. Roster

- [ ] 2.1 Instructions list only the agents in scope, each with its subtree's coverage; tests.

## 3. UI and docs

- [ ] 3.1 Web agent form and team page show the scope; TUI agent view.
- [ ] 3.2 `docs/agent-configuration.md`, `docs/factory-mcp.md`.

## 4. Verify

- [ ] 4.1 Replay 2026-11-11 with fakes:
      - the CEO's `factory_task_create` for developer-lite is refused with "give the task to product-manager";
      - the CEO's roster lists only its direct reports;
      - the PM can still assign to developer-lite.
