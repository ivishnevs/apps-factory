Status: proposed 2026-11-06 by the owner; owner decided 2026-11-06: the window is a required field, written
explicitly (no implicit default). Nothing implemented. **Deferred by the owner 2026-11-07** after the research
below; meanwhile the producer of the owner's company runs with a 200k window set by hand (see "Interim setting").

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

## Research (2026-11-07)

Claude Code ([model configuration](https://code.claude.com/docs/en/model-config.md),
[environment variables](https://code.claude.com/docs/en/env-vars.md)):

- `CLAUDE_CODE_AUTO_COMPACT_WINDOW` is the documented way for scripts; it takes precedence over `/autocompact`,
  `--autocompact` and the `autoCompactWindow` setting, so nothing else on the node overrides it.
- Range 100,000 to 1,000,000 tokens, capped at the model's window; a lower value is raised to 100k (a test
  window below 100k is not possible). `k`/`M` suffixes need Claude Code v2.1.304+, and a bare number is tokens
  (`200` is 200 tokens): the driver passes a plain token count. Leading zeros are accepted (checked on 2.1.319:
  `0000200000` is read without a warning; an unusable value prints "isn't a usable size, so Claude Code ignores
  it" in `-p` mode too, so a wrong value is visible in the run log).
- Default without a window: models with a native 1M window (Opus 4.7+, Sonnet 5+, Haiku 5.5, Fable) compact at
  about 967k, which matches the measured 966k → 58k.
- `CLAUDE_CODE_DISABLE_1M_CONTEXT=1` holds every such model at 200k: simpler, but one value for all agents.
- Idle compaction (`CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS`, default 200k, v2.1.290+) compacts an idle conversation;
  its behaviour under the Agent SDK is not documented.
- Not documented: whether a resumed session already above the window compacts on its first call. This is the
  one thing to check on a real run.

Codex (`config.toml`: `model_context_window`, `model_auto_compact_token_limit`, capped at about 90% of the
window): [openai/codex#16033](https://github.com/openai/codex/issues/16033) is open (bug, regression; still
reproduced on 0.162.0 and main): with `model_auto_compact_token_limit` set, auto-compaction does not fire and
sessions fail with "ran out of room in the model's context window" in a loop. The workaround is to leave both keys
unset. So the Codex part waits for a fix (check the node's Codex version against the issue before enabling it);
of the owner's agents only artist-2d runs on Codex.

Consequences for the tasks: 1.1 becomes a smoke run inside 2.2 (window 100k, a resumed session above it); 1.2 is
blocked on #16033; validation's minimum is 100k for Claude.

## Interim setting (2026-11-07)

Until this change is implemented, a window is set per agent without code through `spec.secrets`: a company string
secret `producer_compact_window` = `0000200000` (secrets need at least 8 characters; the leading zeros also keep
the redaction of secret values from masking `200000` in logs) delivered as `CLAUDE_CODE_AUTO_COMPACT_WINDOW` to
the producer of the owner's company (`ilya-75ba24`). The shim passes the environment to Claude Code unchanged.
Implementing this change replaces it: remove the secret use and the secret, set `context_window: 200k`.

Not in this change: rotating conversation sessions with a written handover (to reconsider if compaction alone
loses too much in long conversations); context-mode or other tool-output filtering (measure first with an A/B run).

## Impact

Catalog: `spec.runtime.context_window` (agent-config), runtime options registry. Node drivers: Claude environment,
Codex thread config. Web: context size in the chat and runs, compaction events. Presets and the game team's
catalog. Specs: `agent-config`, `runtime-drivers`.
