## 0. Decision

- [x] 0.1 Shared bot or a bot per company (2026-11-05: a bot per company, the company's token).
- [x] 0.2 Routing (2026-11-05): reply → that message, `@name` → that agent, plain text → reply to the last
  delivered message. Escalations to owners and topics mode later are defaults.

## 1. After the decision

- [x] 1.1 Bot token sealed under the company key (`telegram_bots`, migration 0020), checked with getMe; a notifier
  per company bot; webhook with a secret on an https address, polling otherwise; `FACTORY_TELEGRAM_TOKEN` stays the
  default company's bot; tests (`TestTelegramCompanyBot`).
- [x] 1.2 Binding per person (t.me link with a 15-minute code, `/disconnect`, Disconnect on the website, a person who
  leaves is disconnected); questions to owners, other alerts to owners and admins, digest to everyone who keeps it;
  tests.
- [x] 1.3 Routing: sender header; reply = exact; `@name`; plain text = reply to the last delivered message with a
  confirmation of where it went; `/inbox`, `/digest`; tests (`TestTelegramRouting`, `TestTelegramInbound`).
- [x] 1.4 Web Settings → Integrations (bot token with BotFather steps, Connect Telegram, digest switch, Disconnect);
  browser check locally (adding a bot with a token Telegram refuses shows the error at the field; connecting needs
  a real bot and a session: on the sandbox by the owner); docs (`messaging.md`, `owner-ui.md`, `deploy-railway.md`).
