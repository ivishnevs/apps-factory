# Adding a machine

Goal: a second machine runs the company's agents as a node.

Why not a plain LAN URL: a node refuses a Core URL that is not https unless Core is on the same machine
(`http://127.0.0.1`, `localhost`): its requests are signed, but the connection would still be readable and
replaceable on the network. A self-hosted Core on a laptop listens on loopback, so the second machine needs an https
address that reaches it.

Choose the route:

- Hosted Core (an https address already): use it as is.
- Both machines on Tailscale (recommended for a self-hosted Core): on the Core machine
  `tailscale serve --bg --https=443 http://127.0.0.1:8080` gives `https://<machine>.<tailnet>.ts.net` with a valid
  certificate, reachable only inside the tailnet. Check from the second machine:
  `curl -s https://<machine>.<tailnet>.ts.net/health` answers 401 (Core is reached; it wants a credential).
- Same LAN without Tailscale: an SSH tunnel from the second machine, `ssh -N -L 8080:127.0.0.1:8080 core-host`, and
  `FACTORY_URL=http://127.0.0.1:8080` there (the node sees loopback). The tunnel must stay up (a launchd/systemd unit).

Then on the second machine:

1. Install and connect: `curl -fsSL https://<core>/install.sh | sh` (sign in), or with a code from
   `factory_node_enrollment`: `... | sh -s -- --code CODE --name mac2`.
2. Approve it: compare the code and fingerprint the machine shows, then `factory nodes connect ID` or
   `factory_node_approve node=ID fingerprint=...`. Approve only a machine you just ran this on.
3. Its own runtime logins: runbook `node-logins` (`factory-node login claude`).
4. A machine on a corporate VPN or proxy: check `net.dns` / `net.api` now (runbook `dns-and-proxy`).
5. macOS: keep it awake while it works (System Settings → Battery → prevent sleep on power adapter), or runs end
   `lost`.

## Proof

- `factory-node doctor` on the new machine passes: no `fail` line, `net.api` reaches the runtime's API host.
- `factory_status` lists the node online with its drivers usable.
- A run starts on it: `factory_runs` shows a run with `node_id` of the new node (pause the other node briefly with
  `factory_pause scope=node` if needed, and resume it afterwards).
