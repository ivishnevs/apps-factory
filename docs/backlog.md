# Backlog

## Known issues

### NATS does not isolate companies (must be solved before the first external node)

Nodes connect to one shared NATS account. A security review on NATS 2.11.4 showed that subject permissions do not
stop a modified node: the server does not check the reply address of a permitted request, and its own answer can
trigger server API actions. In that test, a node with permissions for one company damaged the consumers and streams
of the others. A holder of any node's NATS login can do this. Secrets do not pass through NATS: they go in the signed
HTTP claim answer. Revoking a node (`node-identity`) does not cut its NATS login.

**Current risk:** low while only our own nodes connect and NATS is not reachable from outside.

**Rule:** do not expose NATS to, or give a NATS login to, a machine we do not control until this is solved.

**Decision** (owner, 2026-11-04, later the same day): remove NATS; nodes use only signed HTTPS with long polling.
Planned in the OpenSpec change `http-node-transport` (not started). The rule above holds until it lands.
Options considered: HTTP with long polling and NATS removed (chosen, the recommendation of that review); nodes only
listen on NATS in an account without JetStream; one NATS account per company.

## Optional work

Optional work moved out of finished OpenSpec changes. Nothing here is planned; give an item its own
OpenSpec change when it is picked up.

## Terminal UI (from `owner-tui`)

- 6.7 Feed start without full history replay: start from a recent window and page back on demand. Verify: client test with a large feed.
- 6.8 Connection profiles: several Core URLs / companies with a switcher instead of one env file. Verify: config test.
- 6.9 Form for multi-field questions (options per field, free text). Verify: inbox test; golden.
- 6.10 Help overlay that scrolls on terminals under 30 rows. Verify: golden at 80x24.
- 6.11 Live CI status of a task's PR (not only the checks at submission). Verify: client test with fake forge data.
- 6.12 glamour markdown once the build image moves past Go 1.25.8. Verify: artifact preview golden.

## Company self-service (from `company-self-service`)

- 6.2 Terminal UI: read-only view of the catalog (optional).
