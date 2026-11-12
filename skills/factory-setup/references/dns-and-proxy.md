# DNS and proxy faults

Goal: the node resolves and reaches the runtime's API host (api.anthropic.com for Claude, chatgpt.com for Codex) and
the forge (github.com) through the system resolver, as the runtimes do.

How it shows: `factory-node doctor` fails `net.dns` (the name does not resolve) or `net.api` / `net.forge` (no
connection); the driver is disabled with that cause, so runs stay queued instead of failing (`factory_status`:
"claude: disabled, probe net.dns ..."). A run that still ended with `ENOTFOUND` or `ECONNREFUSED` for the API host is
counted as the node's fault: its task keeps its attempts and goes to another node.

Usual causes, most likely first:

1. Cloudflare WARP or another corporate VPN with its own DNS or a filtering policy: disconnect it, or exclude the API
   hosts (split tunnel), then check again.
2. A stale Tailscale resolver (MagicDNS after the tailnet changed, `100.100.100.100` answering nothing):
   `tailscale up --accept-dns=false`, or restart Tailscale; check `scutil --dns` (macOS) / `resolvectl status` (Linux).
3. A proxy the runtimes do not use: set `HTTPS_PROXY` for the node (node.yaml `runtime_env` lets it through to runs),
   or remove the proxy requirement for the API hosts.
4. A hosts file or local DNS filter (Pi-hole, NextDNS) blocking the hosts.

Check by hand on the node, as the node's user:

```sh
dscacheutil -q host -a name api.anthropic.com    # macOS; Linux: getent hosts api.anthropic.com
curl -sI https://api.anthropic.com | head -1     # any HTTP status line means it is reached
```

## Proof

- `factory-node doctor` passes `net.dns`, `net.api` and `net.forge`.
- The driver is usable again in `factory_status` (the node re-checks at its next probe; restarting the node checks
  at once), and a run of that runtime starts on the node (`factory_runs`).
