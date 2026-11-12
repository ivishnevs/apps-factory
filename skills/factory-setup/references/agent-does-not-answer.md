# An agent does not answer

Goal: the owner's message to an agent gets an answer, or the owner knows exactly why not.

1. Ask: `factory_why agent=AGENT project=PROJECT` (the agent's chat in the project) or `factory_why task=ID`. The
   answer names the cause, the fix and the runbook. Typical causes:
   - a pause of the company, project, agent or node → runbook `pauses`;
   - the node's disk below its critical watermark → runbook `disk` (conversations still run above 1 GiB);
   - a driver disabled on every node (`driver_disabled`: login, `net.api`) → runbooks `node-logins`,
     `dns-and-proxy`;
   - tied to a node: the agent's session (or the task's unsaved work) is on a node that is paused, offline or
     unable. Bring that node back (`factory_resume scope=node`), or release the session so another node starts a
     fresh one: the web's Nodes page, Release now (owner API `POST /nodes/{id}/sessions/release`);
   - no node online or registered → runbook `adding-a-machine`;
   - waiting for an automatic retry after failed runs: `factory_runs failed=true` shows why they failed;
   - agent busy, no free slot: it starts when the current run ends; nothing to fix.
2. The run started but ended without an answer: `factory_runs` shows its outcome and cause (`no_exit`,
   `idle_timeout`, `lost`, a node fault). `factory_node_log` on the node shows the runtime's errors.
3. Nothing waits and no run exists: the message may have gone to another agent; `factory_status` and the chat on the
   web show where it went.

## Proof

- `factory_why` for the chat says "running now" or "runnable now", and `factory_runs` shows its run RUNNING.
- The agent's reply appears in the chat (web, Telegram or `factory conversations`).
