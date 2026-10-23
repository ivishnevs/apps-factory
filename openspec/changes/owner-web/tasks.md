## 1. API
- [x] 1.1 `factory web` over `tui.Client`: reads, actions with the `X-Factory-Web` header, SSE relays, `--demo`/`--fixture`. Verify: `go build ./...`.
- [ ] 1.2 API tests against the demo fixture: every read, an action, the header check, a stream.

## 2. Web app
- [x] 2.1 Next.js app shell: providers, sidebar and phone tab bar, typed API client, confirmation dialog, status badges, route stubs. Verify: `npm run build`.
- [ ] 2.2 Home, Tasks board, Task detail with owner actions.
- [ ] 2.3 Run view (live transcript), Activity feed (live), Inbox (answers, approvals with artifact preview, escalations).
- [ ] 2.4 Chat with direct reports, Costs, Nodes with pauses and the kill switch, create-task form.

## 3. Delivery
- [ ] 3.1 Run on the owner's Mac over Tailscale; docs (`docs/owner-web.md`, README).
