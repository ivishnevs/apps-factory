## 1. Verify

- [ ] 1.1 Claude Code: CLAUDE_CODE_AUTO_COMPACT_WINDOW compacts a resumed session at the set size (small test
      window, real run); the compaction event shows in the session.
- [ ] 1.2 Codex: the auto-compact token limit set per thread through app-server, same check.

## 2. Catalog and node

- [ ] 2.1 `spec.runtime.context_window` (agent and profile, k/M suffixes), validation per provider; runtime option
      `context_window`; tests.
- [ ] 2.2 Claude driver: the run's environment; Codex driver: thread config override; tests.

## 3. Defaults and visibility

- [ ] 3.1 Preset defaults (owner confirms numbers); update the game team's catalog.
- [ ] 3.2 Context size against the window in the chat and runs; compaction events in the conversation.
- [ ] 3.3 Measure the producer's conversation before and after (context per call, cache-read tokens per run).
