Status: proposed 2026-11-08 by the owner ("run limits and the context window set separately for work and for
communication: work runs stay at their maximum, a conversation can always run next to them, conversations get a
smaller context; the chat says why an agent cannot answer"). Nothing implemented.

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

## Open questions

1. **Node slots.** A conversation run still takes a node slot. When every slot is busy with work, the conversation
   waits (case 1 moves from the agent limit to the node). Options: (a) a per-node `conversation_slots` (default 1)
   on top of `slots`, used only by conversation runs; (b) conversations may exceed `slots` by one; (c) no change.
   Suggested: (a), conversation runs are light (no worktree, no writer slot, mostly model time).
2. **Session affinity for conversations.** A conversation session is pinned to the node that holds it; if that node
   is full or paused, the conversation waits up to the affinity timeout (90 min). Should conversations fall back to
   a fresh session elsewhere after a short wait (e.g. 5 min), relying on compaction summaries and Factory history?
3. **Cross-project conversation limit default.** One conversation per project needs no limit; an agent in many
   projects could run many at once. Default unlimited, or equal to the work limit?
4. **Relation to agent-context-window.** That change is deferred by the owner. Implement only the per-kind window
   here (optional field, no required-explicit migration), or revive it with the per-kind shape?
5. **Role defaults (throughput-signals 1.1).** Role concurrency defaults apply to the work limit only; confirm.

## Impact

Agent config (`execution.conversation`, `runtime.context_window` per kind), run counting in storage (evaluate,
scheduler, node claim, queued reasons), node drivers (window per run kind), conversation task clearer text, chat and
team page (web, TUI). Specs: `task-engine`, `agent-config`. Related: `agent-context-window` (window shape),
`throughput-signals` (role defaults, idle-capacity signal), `session-affinity`, `node-readiness`.
