# Backlog

## Known issues

None open. The NATS company-isolation issue (a node's NATS login could damage other companies' streams, and a
revoked node kept its login) is closed by `http-node-transport`: NATS is removed and nodes reach Core only through
signed HTTPS requests, checked per company and per node.

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
