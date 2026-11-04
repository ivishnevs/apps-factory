Status: proposal — needs the owner's decision (raised 2026-11-05 in saas-web-ui, P2). Nothing is implemented.

## Why

Telegram notifications and answers use one bot per Core (`FACTORY_TELEGRAM_TOKEN`) bound to the default company only.
In the SaaS every company and person needs them, and the web has no Telegram settings.

## What Changes (proposed)

- One shared bot for the whole service; Settings → Integrations issues a bind code per company and person
  (`/start <code>` in the bot); a chat is bound to one membership.
- Notifications, digests and answers go per membership; unbinding from the web or with `/stop`.

## Decisions needed

1. One shared bot (this proposal) or a bot per company (the company brings its own token)?
2. Per person (each member binds their chat) or per company (one chat for the owners)?

## Impact

`internal/notify/telegram`, `internal/core/telegram.go` (state per company and membership), web Settings →
Integrations, `docs/messaging.md`.
