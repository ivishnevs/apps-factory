## 1. Limits per run kind

- [ ] 1.1 Agent config: `execution.conversation.max_concurrent_runs`; `AgentCaps` returns work and conversation caps;
      validation and catalog round trip; tests.
- [ ] 1.2 Count live runs by kind (run spec `conversation`) in task evaluation (`agentLive`), the scheduler, the node
      claim and queued-run reasons; a conversation no longer waits behind work; tests for both directions.
- [ ] 1.3 Node `conversation_slots` (default 1): heartbeat reports them, the claim counts conversation runs against
      them first, then against work slots; work runs never use them; tests.
- [ ] 1.4 Conversation affinity: a queued conversation run waits at most `session_affinity.conversation_wait`
      (default 10 min) for the node holding its session, then starts a fresh session on any node; the brief carries the
      recent conversation history; tests.

## 2. Context window per kind

- [ ] 2.1 `runtime.context_window` accepts a value or `{work, conversation}`; validated against the provider.
- [ ] 2.2 Node drivers set the compaction window from the run kind (Claude env, Codex thread config); tests.
- [ ] 2.3 Owner's company: `context_window: {work: max, conversation: 200k}` on every agent; remove the producer's
      `producer_compact_window` secret use and the secret.

## 3. Why it cannot answer

- [ ] 3.1 Conversation task clearer names the cause in plain words (live conversation run and the senders it
      handles, cross-project limit and where, node full or paused, shared work limit).
- [ ] 3.2 Chat shows that line under an unhandled message (web, TUI); team page shows limits, live and queued runs
      per kind and both windows.

## 4. Verify

- [ ] 4.1 Replay 2026-11-08 on a sandbox: network-dev with work limit 1 and one live work run answers a producer
      question within a minute; the producer's spec run keeps the large window while its conversation compacts at
      200k; the chat shows the reason while the answer waits.
