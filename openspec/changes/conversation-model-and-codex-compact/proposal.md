Status: implemented 2026-11-18 (proposed and decided the same day by the owner: "a model setting for communication,
optional so current companies don't break; if unset the agent's main model is taken"; "a compaction workaround for
Codex that honours our settings, and skips when the window is the default max so the agent compacts by itself").
Applying it to the owner's company (conversation models, the Codex conversation window) is the owner's step after
deploy (tasks 3.x).

## Why

Two gaps of work-and-conversation-runs, measured in the owner's company on 2026-11-18:

- **Conversations run on the work model.** Conversation runs are mostly routing and short answers (the producer's
  conversation had 1234 runs, game-designer's 425), yet they run on the agent's work model (Opus, or Codex's
  default). The per-kind split exists for the context window but not for the model.
- **A Codex conversation cannot have a window.** The Codex driver passed the window as the thread's
  `model_auto_compact_token_limit`, and with that limit set Codex never compacts (openai/codex#16033, still open,
  reproduced on 0.162–0.166). So Codex agents had to keep `context_window: max`, and artist-2d's conversation session
  grew to ~870k tokens per run: 70 runs of "read and record" acknowledgements re-reading the whole session, about
  123M tokens since 11-05, which exhausted the Codex subscription and paused artist-2d's real tasks.

## What Changes

- **Optional conversation model per agent.** `spec.runtime.conversation_model` (agent or profile), a model of the
  agent's provider (validated like `model`). Conversation runs use it; work runs keep `model`. Unset (every existing
  catalog) keeps today's behaviour: conversations run on `model`. A task's model override still wins for its task.
  The team page shows it, the agent editor edits it ("Same as the model" by default), presets carry it while the
  provider stays.
- **Codex compacts through the driver.** The Codex driver no longer sends `model_auto_compact_token_limit`. With a
  window for the run's kind, after the run's last turn it compares that turn's context (`tokenUsage.last.totalTokens`)
  with the window and, at or above it, calls `thread/compact/start` (present in 0.162.1) and waits for the compaction
  turn (its `thread/compacted` / `contextCompaction` item and `turn/completed`, at most 5 minutes). Best effort: a
  refusal, a disconnect or the timeout is logged and the run's result is the turn's. The compaction's tokens count
  towards the run. Without a window (`max`, unset) nothing changes: Codex compacts at its own limit.
- **Node gate.** Nodes list the feature `codex_manual_compact`; Core drops `context_window` from a Codex run claimed
  by a node that does not list it (its older driver would pass the limit to Codex and turn compaction off).

## Decisions

- **After the turn, not before the next.** `thread/resume` does not report the thread's size, only a turn does. So
  the driver compacts at the end of the run that outgrew the window; the next run starts small. The first run after
  deploy of an already large session pays one compaction at its end.
- **Conversation model on the agent, not per task kind table.** One optional string next to `model`, the same shape
  as the existing per-kind window, no new option on the wire: Core resolves the run's `model` before dispatch, so
  nodes need no change for it.
- **Limits stated.** A team profile change of provider leaves an agent's `conversation_model` of the old provider
  invalid: the catalog edit is refused with the field named, the owner clears or changes it. The web editor clears
  it when the runtime is switched.

## Follow-up

- **openai/codex#16033 fixed:** pass the window again as `config.model_auto_compact_token_limit` in the Codex driver
  (marked `TODO(openai/codex#16033)` in `internal/driver/codex/codex.go`), drop `session.compact` and the
  `codex_manual_compact` node feature gate (`storage.FeatureCodexCompact`, `runner/v2.go`), and the doc note.

## Impact

- `internal/config` (field, validation, `RunOptions`), `internal/core` (dispatch, team view, field errors),
  `internal/presets` (copy overrides), `internal/driver/codex` (compaction), `internal/storage/nodes.go` and
  `internal/node/runner/v2.go` (feature gate), web agent editor and team card, `docs/agent-configuration.md`.
- No migration: the field is optional and omitted when empty, so run snapshots and config hashes of existing agents
  do not change.
