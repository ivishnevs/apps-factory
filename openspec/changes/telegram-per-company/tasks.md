## 0. Decision

- [x] 0.1 Shared bot or a bot per company (2026-11-05: a bot per company, the company's token).
- [x] 0.2 Routing (2026-11-05): reply → that message, `@name` → that agent, plain text → reply to the last
  delivered message. Escalations to owners and topics mode later are defaults.

## 1. After the decision

- [ ] 1.1 Bot token as a company secret; webhook per company; tests.
- [ ] 1.2 Binding per membership (link with code, unbind, `/stop`); delivery by role and addressee; tests.
- [ ] 1.3 Routing: sender header; reply = exact; `@name`; plain text = reply to the last delivered message with a
  confirmation of where it went; `/inbox`; tests.
- [ ] 1.4 Web Settings → Integrations (bot token, connect my Telegram, digest on/off); browser check; docs.
