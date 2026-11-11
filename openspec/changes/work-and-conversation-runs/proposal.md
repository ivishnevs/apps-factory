Status: implemented 2026-11-10 (2.3 applied in the owner's company 2026-11-10: Claude agents {work: max, conversation: 200k}, artist-2d max, the producer_compact_window secret removed; conversation runs verified with 200000); proposed 2026-11-08 by the owner ("run limits and the context window set separately for work and for
communication: work runs stay at their maximum, a conversation can always run next to them, conversations get a
smaller context; the chat says why an agent cannot answer"). Open questions decided the same day (see Decisions).
Implemented 2026-11-10 (see "Implementation"), except 2.3 (the owner's company settings, applied by the owner after
deploy).

## Why

An agent has two kinds of runs, and Factory treats them as one:

- **Work runs**: one session per (task, agent), long, heavy (builds, tests, large diffs).
- **Conversation runs**: one standing conversation task per (agent, project), `conv-<agent>-<project>`. Every message,
  question, review request and escalation addressed to the agent, from the owner, its manager or a peer, and every
  "delegated task closed" notice wakes that one session (`deliverTx`). The runs are short; the session lives for good.

Seen on the owner's company (2026-11-08):

1. **A conversation waits behind work.** `max_concurrent_runs` counts both kinds (`agentLive`, the claim check in
   `nodes.go`, the scheduler). With network-dev at limit 1 and one work run live, `conv-network-dev-gnome-arena`
   sat `capacity` behind four more work tasks: a one-minute answer to the producer waited for hours. Producer
   questions to agents expired and were forwarded to the owner ("forwarded to owner because you did not answer it in
   time").
2. **Raising the limit to fix (1) also raises parallel work**, which the owner may not want (one Mac, CPU-heavy
   gates). There is no way to say "at most 2 work runs, and a conversation can always run".
3. **One context window for both.** The producer's conversation grew to ~966k tokens in one session
   (agent-context-window). The interim fix (`CLAUDE_CODE_AUTO_COMPACT_WINDOW` = 200k through a secret) applies to
   every producer run, also to spec and plan work where a large window helps. Other agents' conversations have no
   limit at all.
4. **The chat does not say why an agent is silent.** The conversation task carries `reason: capacity` and a clearer
   ("wait for the agent's current run"), but the chat shows nothing; the owner cannot tell "busy with work", "already
   answering someone else" and "no node" apart.

## What Changes

- **Two run kinds in the agent config.** `spec.execution` keeps `max_concurrent_runs` (and `_per_project`) for
  **work** runs only; a new `spec.execution.conversation.max_concurrent_runs` limits **conversation** runs across
  projects (default: one per project, i.e. no extra limit, since each project has exactly one conversation task per
  agent and its runs are sequential by construction). Conversation runs no longer count against the work limit.
  Every place that counts live runs (task evaluation, scheduler, node claim, queued-run reasons) counts by kind,
  using the run spec's existing `conversation` flag.
- **Context window per kind.** `spec.runtime.context_window` becomes `{work, conversation}` (a single value still
  means both). The node sets the compaction window per run from the kind (Claude: `CLAUDE_CODE_AUTO_COMPACT_WINDOW`;
  Codex: the auto-compact token limit). This replaces the producer's interim secret. Defaults per kind come from
  agent-context-window's rule (explicit values, written by presets and the editor); the suggested values are work =
  model maximum, conversation = 200k.
- **"Why it cannot answer" in the chat.** When a message to an agent is not being handled, the chat shows one line
  under it from the conversation task's state, in plain words, for example:
  - "answering now: messages from producer and gameplay-dev (started 2 min ago)": a conversation run is live and
    the new message is queued for its next run;
  - "waiting for a free conversation run: busy in Gnome Arena 2 and Tools" (the cross-project conversation limit);
  - "waiting for a node: hetzner-hel1 and the Mac are full" or "its session is on the paused node Juliya";
  - "waiting for the agent's work run" only if the owner set conversations to share the work limit.
  The same text is the conversation task's clearer, so the board, the API and the producer's tools show it too.
- **Team page.** Each agent shows work and conversation limits, live and queued runs per kind, and both windows.

Not in scope: several parallel sessions of one conversation (a conversation stays one session, its messages are
batched into the next run); per-sender conversation threads; cost budgets.

## Decisions (owner 2026-11-08, on Claude's suggestions)

1. **Node slots: conversation slots on top of work slots.** A node gets `conversation_slots` (default 1) besides
   `slots`; conversation runs take a conversation slot first and a work slot only when none is free. Work runs never
   use conversation slots. Conversation runs are light (no worktree, no writer slot, mostly model time), so a full node
   still answers.
2. **Conversations do not wait long for their node.** The sticky rule stays: a conversation run goes to the node
   holding its session. It waits for that node at most 10 minutes (`session_affinity.conversation_wait`, company
   setting, owner 2026-11-08), whether the node is inactive (paused, offline) or active without a free slot; then it
   starts a fresh session on any node with a free slot, and the brief carries the last compaction summary or the
   recent conversation history from Factory. Work runs keep the existing rules (wait while the holder is active,
   release 90 minutes after it goes inactive).
3. **Cross-project conversation limit: none by default.** Each project has one conversation task per agent and its
   runs are sequential; node conversation slots bound the total. `execution.conversation.max_concurrent_runs` stays
   available for an owner who wants a cap.
4. **Window per kind is optional here.** `runtime.context_window` (one value or `{work, conversation}`) is an optional
   field; without it the runtime's default applies, as today. agent-context-window stays deferred; if revived, it
   takes this per-kind shape. The owner's company sets `{work: max, conversation: 200k}` for every agent once this
   ships, replacing the producer's secret.
5. **Role defaults (throughput-signals 1.1) apply to work runs only.**

## Impact

Agent config (`execution.conversation`, `runtime.context_window` per kind), node config (`conversation_slots`),
company setting `session_affinity.conversation_wait`, run counting in storage (evaluate, scheduler, node claim, queued
reasons), node drivers (window per run kind), conversation task clearer text, chat and
team page (web, TUI). Specs: `task-engine`, `agent-config`, `node-agent`. Related: `agent-context-window` (window shape),
`throughput-signals` (role defaults, idle-capacity signal), `session-affinity`, `node-readiness`.

## Implementation (2026-11-10)

Decisions taken while implementing, beyond the proposal:

- **Shared limit as a switch.** "Conversations share the work limit" is `execution.conversation.share_work_limit:
  true`: both kinds then count against `max_concurrent_runs` (and the per-project cap) as before the change. Without
  it a conversation run has no per-project cap (one conversation task per project, its runs sequential) and
  `execution.conversation.max_concurrent_runs` 0 means no cap (Decision 3).
- **Counting.** Storage counts each kind with the run spec's `conversation` flag (`AgentRunCaps(...).For(kind)`,
  `agentLive`/`agentStarted` with a kind filter). `AgentCaps` keeps its signature (work caps) for other callers;
  `tasks.Snapshot` gained the conversation counts and caps, and `Evaluate` applies the caps of the task's kind.
- **Conversation slots are declared at registration**, like `slots` (node.yaml `conversation_slots`, default 1, 0..64),
  not in the heartbeat. A node that does not send them (older than the change) has **0** conversation slots: its
  conversation runs keep taking work slots, exactly as before (an old node's runner has no extra slot to run them in).
  The claim tells the slots apart by the slot index the node claims with: index >= `slots` is a conversation slot and
  only a conversation run may hold it. A run in a conversation slot uses no resource classes. The poll carries
  `free_conversation` per provider and offers flag conversation runs, so the pool gives them a conversation slot
  first (`OffersFor`; `Offers` stays for callers without conversation slots).
- **Conversation wait.** Counted from when the run was queued (`runs.created_at`), for an active holder too; for an
  inactive holder the earlier of that and the 90-minute release applies. Company setting
  `conversation_wait_minutes` (1 minute to 24 hours, migration 0028), on the existing
  `GET/PUT /settings/company/session_affinity` route (both fields optional on PUT) and on the Settings page.
- **History for a fresh session on another node.** Core has no compaction summary, so the brief opens with the
  conversation's last 20 inputs (triggers) and the last 10 run summaries (results), oldest first, when the claiming
  node is not the node holding the session.
- **Window values.** `context_window` keeps the text as written (`200k`, `1M`, `max`, a token count), validated
  100k–1M for both providers (Claude Code's bounds); `max` or unset sends nothing. The run's window travels as runtime
  option `context_window` (tokens) set by Core at dispatch from the task's kind. Claude: the env variable replaces one
  a secret set. Codex: `model_auto_compact_token_limit` in the thread config; Codex 0.162 does not compact with it
  (openai/codex#16033), documented, not refused.
- **Clearer texts.** A conversation task's clearer (and detail) is plain words: "answering now: messages from A and B
  (started N min ago); newer messages are next", "waiting for a node: its session is on the paused node X; another node
  starts it fresh at HH:MM UTC", "waiting for a node: X and Y are full", "waiting for a free conversation run: busy in
  P1 and P2", "waiting for AGENT's work run (conversations share its limit)", "starting: ...". Queued runs reuse
  `QueuedWhy` (which now carries `holder` and `inactive` for affinity). New wait kind `agent_conversation`.
- **Web (owner 2026-11-10: everything configurable in the web).** Agent editor and Add agent: work and conversation
  runs at a time and the context window (one value or separate work/conversation), validated as Core does, errors
  next to the fields (Core maps its errors to fields `context_window`, `conversation_max_runs`,
  `max_concurrent_runs`); preset overrides carry the same fields, and a provider override keeps the preset's window.
  Settings: a Sessions section with the release timeout and the conversation wait. Nodes: work and conversation
  slots per node and runs per kind (`GET /agents/runs`); slots stay read-only with the node.yaml hint, since Core
  cannot push node settings. Team page: limits, live and queued runs per kind and both windows. Chat (web, TUI): the
  clearer under the owner's unanswered message.
- **Migrations.** 0028_work_conversation_runs.sql; 0027 is node-readiness's (developed in parallel). A placeholder 0027
  is committed separately so this branch's tests run; drop it when merging node-readiness.
- 4.1 is a Go integration test (`internal/core` TestReplayConversationBehindWork), no live sandbox.
