Status: proposed 2026-11-06 by the owner; owner decided 2026-11-06: the window is a required field, written
explicitly (no implicit default). Nothing implemented.

## Why

Agents run with the model's full context window (1M tokens for the default Claude model) and Factory never limits
it. Sessions continue across runs: the session key is (task, agent), so every rework round resumes the developer's
session of that task, every re-review resumes QA's, and every message of a conversation resumes one session for
good. Claude Code compacts only at the edge of the window.

Measured on the owner's company (2026-11-06): the producer's conversation with the owner is one session of 146 runs
that grew from 28k to ~966k tokens before its single automatic compaction (to 58k; 131k again now). Near the end
every model call re-read ~900k tokens: 256M cache-read tokens over the conversation, ~$65 by Claude's estimate of
subscription value, almost a fifth of all the producer's usage, slower answers, and weaker attention to a very long
tail. Most agents do not need 1M; some (a developer on a large task) may.

## What Changes

- **Context window per agent, required and explicit (owner 2026-11-06).** `spec.runtime.context_window` (tokens,
  e.g. `200k`, or `max`) in the catalog, on an agent or its profile: the size at which the runtime compacts the
  session. Every agent must resolve one; the catalog refuses an agent without it. There is no implicit default:
  presets, the team wizard and the agent editor write the model's maximum explicitly (`max` or the number), so the
  value is always visible and the owner lowers it where wanted. Existing catalogs are migrated once by writing the
  maximum explicitly on each profile. Validated against the provider (a minimum and the model's maximum).
- **The node applies it to every run.** Claude: `CLAUDE_CODE_AUTO_COMPACT_WINDOW` in the run's environment (Claude
  Code clamps it to the model's window). Codex: the auto-compact token limit as a thread configuration override.
  The setting is per run process, so parallel runs of one agent each keep their own context under the same limit;
  on a resumed session the runtime compacts as soon as the session passes it, in whichever run that happens.
- **Presets start at the maximum.** Presets and new agents carry the model's maximum explicitly; lower values
  (e.g. 200k for conversations and leads, 400k for developers) are the owner's choice per agent, suggested in the
  editor, not applied silently.
- **Visible context.** The chat and the agent's runs show the session's current context size against its window
  (the TUI already reads it from the runtimes, `internal/tui/ctxusage.go`), and a compaction appears in the
  conversation as an event ("context compacted: 966k → 58k").

Compaction keeps a summary, not the full history; exact details of earlier rounds stay available to the agent from
Factory (task description, comments, review notes, history) through its tools.

Not in this change: rotating conversation sessions with a written handover (to reconsider if compaction alone
loses too much in long conversations); context-mode or other tool-output filtering (measure first with an A/B run).

## Impact

Catalog: `spec.runtime.context_window` (agent-config), runtime options registry. Node drivers: Claude environment,
Codex thread config. Web: context size in the chat and runs, compaction events. Presets and the game team's
catalog. Specs: `agent-config`, `runtime-drivers`.
