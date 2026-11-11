## 1. Task kinds (decided 2026-11-11)

- [x] 1.1 `kind: work|spec` on tasks, the API and the lead tools (schema with both described); an agent linking a
      change must set it; a person's task defaults to work; update changes it; a conversation keeps its kind; tests.
- [x] 1.2 A spec task's run carries no change, so the node does not check it (no node update); the brief tells a
      spec task to write the change; tests.
- [x] 1.3 `change_missing` answers `spec`, `retry`, `cancel`; `spec` makes the task a spec task and runs it again;
      labels in web Inbox, TUI and Telegram; tests.
- [x] 1.4 Web: a spec task's link shows "Writes spec"; docs (openspec, task-lifecycle).

## 2. Verify

- [ ] 2.1 Owner's company: the eight P3 spec tasks run after the answer `spec` (or the producer's kind update) and
      finish with their changes merged.
