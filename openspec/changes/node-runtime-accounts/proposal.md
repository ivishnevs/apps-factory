Status: proposed 2026-11-06; the owner agreed to options 1 and 2 below. Nothing implemented.

## Why

A node runs the agents as the machine's user with whatever Claude Code and Codex login that user has right now:
the same `~/.claude` / macOS Keychain entry and `~/.codex` as the person's own terminal. The node checks only that
a subscription login exists (Codex: a ChatGPT account, Claude: not an API key) and never which account it is.

When the person logs in to another subscription for their own work — a common case: personal and work plans on
one laptop — every next agent run silently uses it:

- the company's work spends another account's limits (an employer's Team plan for a personal company), which may
  break that account's terms;
- the person's own sessions and the agents share and exhaust one account's limits;
- nobody is told: the owner learns from a bill or a limit.

## What Changes

1. **The node has its own runtime logins.** The node keeps Claude Code's and Codex's configuration and credentials
   in its own directories (`CLAUDE_CONFIG_DIR`, `CODEX_HOME` under `~/.app-factory/runtimes/…`) and passes them to
   every run. The person logs the node in once with `factory-node login claude|codex` (the runtime's own login
   flow, run against the node's directory); logging in or out in their terminal no longer changes the agents.
   `factory-node doctor` and the Connectors page say which account (type of plan, organisation; never the email)
   each runtime of the node uses. Existing nodes: the installer offers to copy the current login once, or to log in
   anew; until then runs keep the shared login and doctor warns.
2. **The node remembers the account and stops on a change.** At login (or the first run) the node records a
   fingerprint per runtime: plan type, organisation id and a hash of the account email. Before each run it reads the
   runtime's account (Claude `accountInfo`, Codex `account/read`, no model call) and compares. A different account
   pauses that runtime on the node (its runs stay queued, other runtimes go on) and puts an item in the owner's
   Inbox and Telegram: "Claude on mac-main now uses organisation X (Team), it used your personal Max. Continue with
   it / Keep paused". Continuing records the new fingerprint. A logged-out runtime is reported the same way.

Not in this change (owner 2026-11-06: the node's own login plus the change check are enough): limits and usage per
account; a company choosing which account its work may use on a shared node; several accounts per runtime on one node.

## Impact

Node: runtime config directories and login command, account fingerprint and per-run check, pause of one runtime,
doctor; installer migration. Drivers: Claude shim and Codex app-server get the config directories. Core: Inbox item
and Telegram notice for an account change, Connectors page. Spec: `runtime-drivers`.
Related: `connector-trust` (what the node may do as the user), `hosted-connectors` (own vs cloud work).

## Open points

- Claude Code on macOS keeps the login in the Keychain; with its own `CLAUDE_CONFIG_DIR` it must use a separate
  Keychain entry. To verify on the owner's Mac before building (task 1.1).
- Windows and Linux nodes: the same directories; Claude on Linux keeps the login in the config directory.
