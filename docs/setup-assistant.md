# Setup assistant (setup-assistant)

Factory knows its own state, so it answers the owner's setup and support questions itself instead of a developer
reading Core's code, logs and database. Three parts: the owner MCP server for the owner's own coding agent, the
built-in `factory-setup` skill with runbooks, and the company doctor. The node's network checks keep a machine with
broken DNS from burning attempts.

## Owner MCP server (`factory mcp`)

For the owner's own coding agent (Claude Code, Codex, Claude Desktop), not for company agents (they get Factory MCP,
[factory-mcp](factory-mcp.md)). It runs on the owner's machine over stdio and talks to Core's owner API with
`FACTORY_URL` and `FACTORY_TOKEN` (an owner token, `factory owner-tokens create NAME`; with the operator token
`FACTORY_COMPANY` names the company and is sent as `X-Factory-Company`).

```sh
claude mcp add factory -e FACTORY_URL=https://core.example -e FACTORY_TOKEN=fot_... -- factory mcp
```

Read tools:

| Tool | Answers from |
| --- | --- |
| `factory_status` | projects with pauses and reasons, nodes with disk, drivers and logins, the doctor's problems |
| `factory_doctor` | `GET /doctor`: the company doctor (below) |
| `factory_why` | `GET /tasks/{id}/why`, for a task or an agent's chat (`agent`, `project`): the cause in words, its fix and runbook |
| `factory_runs` | `GET /runs/recent`: recent runs, the cause of each failure, usage explained |
| `factory_catalog_validate` | a dry run of `POST /catalog` on the current version; each error with its fix |
| `factory_agent` | `GET /agents/{agent}/effective`: composed instructions, skills with sources and hashes, runtime and model |
| `factory_runbook` | the factory-setup skill's runbooks from Core's library (`GET /skills/library/factory/factory-setup/files/...`) |
| `factory_node_doctor` | `factory-node doctor` on this machine (`FACTORY_NODE_BIN`, else next to `factory`, else `PATH`) |
| `factory_node_log` | the node log's tail: `FACTORY_NODE_LOG`, else `~/Library/Logs/factory-node.log` (macOS), else `journalctl --user -u factory-node` |

Change tools: `factory_pause`, `factory_resume`, `factory_node_enrollment`, `factory_node_approve`,
`factory_secret_set`, `factory_skill_install`. A call without `confirm` changes nothing: it answers the exact effect
(for a resume, the tasks and runs that start; for an approval, the fingerprint Core holds, refused when the machine's
differs; for a secret, whether it replaces one and which agents use it) and a confirmation code. The code is bound
to the tool and its exact arguments by an HMAC with a key that lives only in this server process, expires after
10 minutes and works once; the owner's agent sends it back only after the owner agreed. No answer carries a secret
value: `factory_secret_set` reads the value from a local file (or takes it as an argument) and answers name, kind and
version; node output is passed through the credential redactor. The enrollment code is the one exception by design:
it is a one-time code the owner needs on the new machine.

## Company doctor (`GET /doctor`, `factory doctor --company`)

Core judges the company now: nodes (offline, paused by the owner), disk (low, critical with free space and watermark),
drivers and logins (each disabled driver with its check and detail), failing node self-checks (`net.forge`, git,
stack checks), pauses, failed runs of the period (`since`, default 24 h) grouped by cause (a node fault kind, else
the outcome), stuck tasks and chats (waiting for something only the owner, a node or a person clears: pauses, node
reasons, secrets, escalations, a run tied to a paused or offline node; not capacity, backoff or a claim on its way)
and pending owner items. Every item has a cause, a fix and a runbook; problems come first.

`factory doctor --company [--since 48h] [--json]` prints it (exit status 1 when it finds a problem). In Telegram,
owners and admins send `/doctor`; the daily digest carries the problems with their fixes (`/digest off` turns the
digest off for a person).

## The factory-setup skill

`skills/factory-setup` is a built-in skill (source `factory`), indexed by Core like the other release skills and
installable for company agents too. Its runbooks: `first-setup`, `moving-in-from-paperclip`, `adding-a-machine`,
`node-logins`, `disk`, `dns-and-proxy`, `agent-does-not-answer`, `pauses`, `token-usage`. Each ends with a `## Proof`
section: the check that proves the fix (a `factory-node doctor` pass, a run that starts). Answers of the doctor and
of `factory_why` name the runbook to follow.

`factory import paperclip` is not implemented yet: no Paperclip export or API format is documented to import
against. The `moving-in-from-paperclip` runbook walks through the move with Factory's own commands meanwhile.

## Node network checks (`net.dns`, `net.api`, `net.forge`)

The Claude and Codex probes resolve the runtime's API host through the system resolver (`net.dns`) and open a
connection to it on 443 (`net.api`): `api.anthropic.com` (or `ANTHROPIC_BASE_URL`'s host; none for Bedrock or
Vertex), `chatgpt.com` for Codex. A failure disables the driver with the cause and the fix, so its runs stay queued
with their attempts (`driver_disabled`); a timeout is transient (retried before it disables). `factory-node doctor`
and the node's periodic self-checks reach the forge (`net.forge`, github.com) and report a failure.

A run that ends with a network error naming the API host (`ENOTFOUND`, `ECONNREFUSED`, `EAI_AGAIN`, `no such host`)
is a node fault of kind `network`, also after its agent used tokens: the task keeps its attempts and is offered to
another node, the node gets none of the project's runs until its readiness re-check, and the node probes its drivers
at once. Side-effect tasks are still parked for inspection, since the agent may have acted before the network failed.
