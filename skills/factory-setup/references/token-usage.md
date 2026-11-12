# Token usage

Goal: the owner understands what a run's token count means and what makes it grow.

`factory_runs` gives each run's `usage_explained`, for example "352k tokens: 340k cache reads (96%: the session's
context read again each turn, billed at about a tenth of new input), 10k new input, 2k output; model ...".

What the parts are:

- Cache reads: on every turn the runtime sends the whole session so far (instructions, the brief, files read, tool
  results); the provider serves the unchanged part from its cache. Long sessions are mostly cache reads. They cost
  about a tenth of new input and count against subscription limits less than new input does.
- Cache writes: new context stored for later turns.
- New input: what was not cached (the first turn, a changed prompt, a resumed session after the cache expired).
- Output: what the model wrote, including tool calls; usually the smallest part and the most expensive per token.

What makes it grow: long sessions resumed again and again (each turn re-reads everything), large files or logs read
into the session, many skills or a long prompt in the instructions (`factory_agent` shows what an agent gets), and
retries of failing runs (`factory_runs failed=true`).

What helps: a fresh session for unrelated work (a new task instead of a long chat), narrower reads, a smaller model for
routine agents (the agent's `runtime.model`), fixing the cause of repeated failures.

## Proof

- `factory costs --task ID` (or `--agent A --since 24h`) shows the cost the owner expects after the change, and
  `factory_runs` for the next runs shows fewer new input tokens or fewer runs.
