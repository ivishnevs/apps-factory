## 1. Design

- [x] 1.1 Hard gate on the task: a reviewer is required on every task outside the backlog (owner 2026-11-06).

## 2. Implementation

- [ ] 2.1 Catalog validation of review coverage; tests.
- [ ] 2.2 Presets and the team wizard: the lead reviews roles without their own reviewer, the lead's work goes to
      people explicitly.
- [ ] 2.3 Core stores an explicit reviewer (agent, owner or person) on every task in review; tests.

## 3. Hard gate on tasks (owner 2026-11-06)

- [ ] 3.1 Task reviewer field (agent, owner or member); Core refuses ready (and create-as-ready, and any later status)
      without it; tool errors name the field and the policy's suggestion; tests.
- [ ] 3.2 Agent tools and instructions: task_create / task_update / task_ready carry the reviewer.
- [ ] 3.3 Web and TUI: reviewer field on the new-task form and the task page, prefilled from the policy; "Reviewer is
      required" on ready without one.
- [ ] 3.4 Migration: fill open tasks from the policy; list the rest to the owner.
