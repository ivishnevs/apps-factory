Status: proposed 2026-11-10 by the owner ("if the account changed, a new session should start as if on a new node;
if the subscription did not change but the session is gone, ask me whether to continue"). Nothing implemented.

## Why

A run resumes its agent's runtime session when the node recorded that session for the same task, agent and directory
(`resumeSpec`, runtime-drivers D1); otherwise it starts a fresh session whose prompt says the context comes from
Core. Two cases are not covered:

1. **The runtime's account changed.** node-runtime-accounts pauses the runtime until the owner continues
   (`factory-node accounts accept`). After that, the node still resumes sessions recorded under the old account. A
   session of one subscription continued under another mixes the two accounts' history and limits, and with the
   node's own `CLAUDE_CONFIG_DIR` after a re-login the transcript may not be there at all.
2. **The session is gone on the node that ran it.** The node has the record but the runtime no longer has the
   session (its config directory was cleaned, the runtime was logged out and back in, the transcript was deleted).
   Today the outcome depends on the runtime: the resume either fails the run or silently starts over. Either way
   the owner is not told that the agent lost its working context mid-task, although that is the moment they may
   prefer to wait (restore the directory, log the node back in) instead of letting the agent start from Core.

Moving a task to another node is not one of these cases: there a fresh session is expected and stays silent.

## What Changes

- **Sessions remember their account.** A node's session record carries the hash of the runtime account it ran
  under (the same fingerprint node-runtime-accounts records). A run whose session was recorded under another account
  starts a fresh session, exactly as on a new node (FreshSessionNote, no question). Records without an account hash
  (made before this change) resume as today.
- **Continuing on a new account resets that runtime's sessions.** The account-change pause stays (decision
  2026-11-06: the company's work must not silently move to another subscription). When the owner continues on the
  new account, every session of that runtime on the node is treated as belonging to the old account, so the next
  runs start fresh; nothing else is needed from the owner.
- **A lost session is a question to the owner.** Before resuming, the node checks that the runtime still has the
  session (Claude: the transcript in the session's project directory under the node's config directory; Codex: the
  rollout under `CODEX_HOME`). When the node recorded the session, the account is unchanged and the session is
  missing, the run is not started: the node reports it as `session_lost` and Core opens an escalation to the owner
  (Inbox, Telegram, TUI):
  "<agent> on <node> lost its session for <task> (the runtime no longer has it). Continue with a new session /
  Wait". *Continue* requeues the run with a fresh session (FreshSessionNote); *Wait* keeps the task's run queued and
  tied to the node, and the next run asks again only if the session is still missing. A resume that the runtime
  still rejects after this check (race, corrupted transcript) is reported the same way instead of failing the run.
- One open `session_lost` escalation per (task, agent); it closes itself when a run of that pair succeeds.

Not in scope: moving sessions between nodes or accounts; restoring a lost transcript; changing session-affinity
timeouts (node-ties).

## Open questions (with recommendations)

1. **Account change without the pause.** Keep the pause and the owner's Continue, then reset sessions
   (recommended; it keeps the 2026-11-06 decision), or let a changed account continue automatically with fresh
   sessions and only notify. Recommended: keep the pause.
2. **Who answers `session_lost`.** The owner (recommended: losing context mid-task is rare and costs the agent's
   working memory), or the agent's manager first, like stalled escalations.
3. **Conversations.** A lost session of a conversation (Lead chat) also asks, or always starts fresh silently
   because the chat history is in Core. Recommended: start fresh silently; only task runs ask.
4. **Timeout for Wait.** Wait holds the run until the owner answers again (recommended), or falls back to a fresh
   session after a time (e.g. 24 h).

## Impact

Node: session records get the account hash; the resume check looks up the session in the runtime's directories;
`accounts accept` resets the runtime's sessions; a `session_lost` run report. Drivers: Claude and Codex locate a
session's transcript or rollout; a rejected resume becomes `session_lost`. Core: `session_lost` escalation with
Continue / Wait, requeue with a fresh session, labels in web, TUI and Telegram. Specs: `runtime-drivers`,
`node-agent`. Related: node-runtime-accounts (account fingerprint), node-ties / session-affinity (session ties),
dependency-wait-escalations (escalation mechanics reused).
