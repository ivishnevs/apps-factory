## 1. Conversation model

- [x] 1.1 `spec.runtime.conversation_model` (agent and profile), validated against the provider like `model`;
      `RunOptions(conversation)` uses it; dispatch keeps a task's model override; tests (config, Core dispatch).
- [x] 1.2 Team view (`conversation_model`), field errors, preset copy overrides (kept while the provider stays).
- [x] 1.3 Web: "Conversation model" in the agent editor ("Same as the model" by default, cleared when the runtime
      changes), shown on the agent card.

## 2. Codex compaction

- [x] 2.1 Codex driver: no `model_auto_compact_token_limit`; compact after the run's last turn when its context
      reached the window (`thread/compact/start`, verified in the 0.162.1 schema), best effort with a timeout, usage
      counted; tests with the fake app-server (over, at, below, no window, refused, stuck).
- [x] 2.2 Node feature `codex_manual_compact`; Core drops `context_window` from Codex runs on nodes without it; test.
- [x] 2.3 Docs: agent-configuration (conversation model; the Codex window now works).
- [ ] 2.4 Verify on a real node after deploy: a Codex conversation with `conversation: 200k` compacts at the end of a
      run above 200k (feed/run events show `factory/compact`, the next run's context is small).

## 3. Owner's company (after deploy, the owner)

- [ ] 3.1 Update Core, then the nodes (Mac, GCP, Hetzner) to the release with this change.
- [ ] 3.2 artist-2d: `context_window: {work: max, conversation: 200k}`.
- [ ] 3.3 `conversation_model` for the agents whose conversations should run on a lighter model (e.g. `sonnet` on
      Claude agents, a smaller `gpt-*` on Codex), then measure tokens per conversation run before and after.

## 4. Follow-up

- [ ] 4.1 When openai/codex#16033 is fixed: restore `model_auto_compact_token_limit` in the Codex driver
      (`TODO(openai/codex#16033)`), remove `session.compact` and the `codex_manual_compact` gate, update the docs.
