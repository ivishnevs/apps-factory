---
name: factory-setup
description: "Set up and keep a Factory company running: first setup, moving in from Paperclip, adding machines, node logins, disk, DNS and proxy faults, an agent that does not answer, pauses and token usage. Each runbook ends with a check that proves the fix."
license: Apache-2.0
metadata:
  categories: operations, setup, support
  eval: "The owner says the CEO does not answer in project tracely. Good: asks Factory why (factory_why agent=ceo project=tracely or GET /tasks/conv-ceo-tracely/why), names the cause it returns (for example a project pause) and its fix, resumes only after the owner confirms, and proves it with a run that starts."
---

This skill is for the owner's own coding agent (Claude Code, Codex, Claude Desktop) helping the owner set up and run
their Factory company. It is not for company agents.

Start from Factory's own answers, not from reading code or logs by hand:

- Owner MCP server: `factory mcp` with `FACTORY_URL` and the owner's `FACTORY_TOKEN` (an owner token from
  `factory owner-tokens create NAME`; `FACTORY_COMPANY` names the company for an operator token). Add it to the agent,
  e.g. `claude mcp add factory -e FACTORY_URL=https://core.example -e FACTORY_TOKEN=... -- factory mcp`.
  Tools: `factory_status`, `factory_doctor`, `factory_why`, `factory_runs`, `factory_catalog_validate`,
  `factory_agent`, `factory_runbook`, and on a machine with a node `factory_node_doctor`, `factory_node_log`.
- Without the MCP server the same answers come from the CLI: `factory doctor --company`, `factory nodes list`,
  `factory pauses`, `factory-node doctor` on the node.

Rules:

1. Ask Factory first: `factory_doctor` for the whole company, `factory_why` for one task or chat. Each answer names a
   cause, a fix and a runbook below; follow that runbook.
2. Changes go through the change tools (`factory_pause`, `factory_resume`, `factory_node_enrollment`,
   `factory_node_approve`, `factory_secret_set`, `factory_skill_install`). Each first returns the exact effect and a
   confirmation code. Show the effect to the owner in their words and call again with the code only after they agree.
   Never guess a code, never reuse one.
3. Never print, paste or log a secret value. Set secrets from a file (`factory_secret_set file=...`) and say only
   their names.
4. Do not poke at Core's database, the node's state directory or tasks by hand. If Factory's answer is wrong or
   missing, say so and propose a fix to Factory instead.
5. A runbook is done only when its proof passes: the check named at its end.

Runbooks (`factory_runbook name=...`, files in `references/`):

- `first-setup` — a new company: Core, owner token, first node, team, first project, a run that starts.
- `moving-in-from-paperclip` — projects, open issues, agent prompts, skills and secrets from a Paperclip company.
- `adding-a-machine` — a second node on the same LAN or over Tailscale `serve` with https; why not a plain LAN URL.
- `node-logins` — the node's own Claude and Codex logins, shared quota, account changes.
- `disk` — watermarks, what may be deleted, how a disk pause ends.
- `dns-and-proxy` — `net.dns` / `net.api` failures: Cloudflare WARP, VPNs, stale Tailscale resolvers, proxies.
- `agent-does-not-answer` — a chat or task without an answer: pauses, ties to a node, disabled drivers, retries.
- `pauses` — company, project, agent, node and quota pauses: who set them, what resuming starts.
- `token-usage` — what the tokens of a run are made of and what makes them grow.
