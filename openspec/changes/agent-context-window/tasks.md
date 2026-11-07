## 1. Verify

- [ ] 1.1 Claude Code: CLAUDE_CODE_AUTO_COMPACT_WINDOW compacts a resumed session at the set size (window 100k,
      the minimum; a resumed session above it; real run, done as the smoke run of 2.2); the compaction event shows
      in the session.
- [ ] 1.2 Codex: the auto-compact token limit set per thread through app-server, same check. Blocked on
      openai/codex#16033 (auto-compaction does not fire with the limit set); check the node's Codex version first.

## 2. Catalog and node

- [ ] 2.1 `spec.runtime.context_window` required (agent or profile; k/M suffixes or `max`), validation per provider;
      runtime option `context_window`; tests including a missing window.
- [ ] 2.3 One-time migration: write `max` explicitly on every profile of existing catalogs.
- [ ] 2.2 Claude driver: the run's environment; Codex driver: thread config override; tests.

## 3. Defaults and visibility

- [ ] 3.1 Presets, wizard and editor write the maximum explicitly; the editor suggests lower values per role.
- [ ] 3.2 Context size against the window in the chat and runs; compaction events in the conversation.
- [ ] 3.3 Measure the producer's conversation before and after (context per call, cache-read tokens per run).
