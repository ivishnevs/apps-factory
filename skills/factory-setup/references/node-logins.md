# Node logins

Goal: every node's runtimes are logged in to the account the owner means, and stay so.

Facts:

- By default a node's agents use the machine user's own Claude Code and Codex logins (`~/.claude` and the Keychain,
  `~/.codex`). Logging in to another account in a terminal then changes the agents' account too; `factory-node
  doctor` warns (`claude.login`, `codex.login`).
- Nodes logged in to the same Claude or ChatGPT account share its quota: two machines on one Max plan hit its limits
  twice as fast. A limit pauses the quota key until it resets (`factory_why` says "subscription limit reached").
- The node records each runtime's account (plan, organisation, a hash; never the email). A different account fails
  `claude.account` / `codex.account` and disables that runtime on the node until the owner accepts it.

Steps:

1. Give the node its own logins: `factory-node login claude` (Claude Code's `/login` against
   `~/.app-factory/runtimes/claude`), `factory-node login codex` (or `-copy-current`). Restart the node.
2. A runtime disabled with `auth` (`factory_status`: "claude: disabled, auth ..."): log in again with the same
   command; the node re-enables it at its next probe after the hold-off.
3. A changed account on purpose: `factory-node accounts accept claude|codex`.
4. Which config dir and account a node uses: `factory-node doctor` (`claude.auth`, `claude.account` lines) and
   `node.yaml` (`drivers.claude.config_dir`, `drivers.codex.home`).

## Proof

- `factory-node doctor` passes `claude.auth` (and `claude.account` when recorded), or the Codex lines.
- `factory_status` shows the driver usable on that node, and the next run of an agent of that runtime starts there.
