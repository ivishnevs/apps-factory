# Pauses

Goal: the owner knows what is paused, by whom and why, and what resuming starts.

Kinds:

- company (`factory stop`, Telegram `/stop`, or `factory pause --all`): nothing new starts anywhere.
- project, agent: `factory pause --project P` / `--agent A`, with a reason everyone sees.
- node: `factory pause --node N` (the web's Nodes page); the node claims nothing. Sessions it holds keep their runs
  waiting for it (keep) unless released (release).
- quota key: set by Core when a subscription limit is hit; it ends by itself when the limit resets.
- disk: not a pause row: Core pauses a node's work below its critical watermark and resumes by itself (runbook `disk`).
- task: an authority's pause of one task (`factory tasks pause|resume ID VERSION`).

Steps:

1. List: `factory_status` (projects with their pause and reason, nodes) or `factory pauses`.
2. Resume with `factory_resume scope=... target=...`. Its preview names the pause, its reason and the tasks or runs
   that start; show it to the owner and resume only after they agree. CLI: `factory resume --project P`,
   `--agent A`, `--node N`, `--quota-key K`, `--all`.
3. A pause the owner does not recognize: its reason and actor are in `factory pauses` and the feed; ask before
   resuming.

## Proof

- `factory_status` no longer lists the pause.
- `factory_why` for a task the preview named no longer says paused, and its run starts (`factory_runs`).
