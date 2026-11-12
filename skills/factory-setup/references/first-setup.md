# First setup

Goal: a company whose agents take work on at least one node.

1. Core. Hosted: the owner signs in on the web and creates the company. Self-hosted: `make up` (docker compose) or
   the Railway deploy (`docs/deploy-railway.md`); Core needs `FACTORY_TOKEN`, `FACTORY_DATABASE_URL` and, for
   secrets, `FACTORY_SECRETS_KEY`.
2. Owner token for the CLI and `factory mcp`: `factory owner-tokens create laptop` (keep it in a password manager;
   never paste it into a chat or a file in a repository).
3. Team: the web's Team page or `factory companies create ID TEMPLATE` from a starter team (`factory templates`).
   Validate catalog changes with `factory_catalog_validate` before pushing them (`factory catalog push DIR`).
4. Secrets the catalog needs: `GET /secrets/needed` (web: Settings → Secrets). Set each with `factory_secret_set
   name=... file=...` or `factory secrets set NAME`. A task whose agent misses a secret waits `secret_missing`.
5. A node: on the machine `curl -fsSL https://<core>/install.sh | sh` (signs in through the web) or
   `factory_node_enrollment` then `... | sh -s -- --code CODE`. Approve it (`factory nodes connect ID` or
   `factory_node_approve` after comparing the fingerprint the machine shows). Remote machines: runbook
   `adding-a-machine`. Logins: runbook `node-logins`.
6. A project: the web's Projects page or `factory projects create`; give it its repository URL so tasks run in
   worktrees.
7. Talk to the team lead in the project's chat.

## Proof

- `factory-node doctor` on the node passes (no `fail` line), including `net.dns`, `net.api` and `net.forge`.
- `factory_doctor` reports no problem.
- A message to the lead starts a run: `factory_runs` lists it as RUNNING or ENDED succeeded within a few minutes.
