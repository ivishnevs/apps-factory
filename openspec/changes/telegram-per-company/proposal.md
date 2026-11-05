Status: proposal — the owner decided part of it on 2026-11-05; the routing between several managers needs the
owner's confirmation (section "Decisions needed"). Nothing is implemented.

## Why

Telegram notifications and answers use one bot per Core (`FACTORY_TELEGRAM_TOKEN`) bound to the default company only.
In the SaaS every company and person needs them, and the web has no Telegram settings. A company with several teams
has several managers (team leads, the PO, agents escalating up the chain) writing to the same person: today free
text goes to "the lead of the project's team", which is ambiguous with more than one team or project.

## Decided (2026-11-05)

- **A bot per company.** An owner or admin creates a bot with @BotFather and pastes its token in Settings →
  Integrations; Core keeps it as an encrypted company secret (core-secret-store) and never shows it again. People
  see the company's own bot name, and a person in two companies talks to two bots, so companies never mix in a chat.

## What Changes (proposed)

- **Per person.** Each member binds their own private chat (Settings → Integrations → "Connect Telegram" gives a
  link `t.me/<bot>?start=<code>`, valid 15 minutes). What a person receives follows their role: questions and
  escalations that reach the owner go to owners (and admins when the escalation names them); messages an agent
  addresses to a person go to that person; everyone may turn the daily digest on or off. `/stop` in the chat or
  "Disconnect" in the web unbinds.
- **Webhooks, not polling.** Each company's bot gets a webhook `https://<core>/telegram/<company>/<secret>` set by
  Core when the token is saved; one Core serves any number of bots without a poller each.
- **Several managers in one chat** (the question the owner raised):
  1. Every message from an agent starts with who and where: **Thread PO · Product team**, then the task or
     conversation it is about. Escalations and questions keep their answer buttons.
  2. **Reply** (Telegram's reply to a message) always goes exactly to that message's sender and conversation: a
     question is answered, any other message continues that conversation. This is the precise way and is shown in
     the bot's help.
  3. **Free text without a reply** goes to the person's current addressee, shown in the bot's answer
     ("→ Thread PO · Product team"). The addressee is set by the last reply or `@name`, and `/to` lists the team
     leads as buttons to switch. With no addressee yet: a company with one team lead sends it there; otherwise the
     bot asks "Who is this for?" with the team leads as buttons and sends it after the tap. Nothing is guessed.
  4. `/inbox` lists what waits for the person (questions, escalations) with buttons to open each.
- **Later, optional:** a Telegram group with topics, one topic per team, for companies that prefer a shared chat;
  a message in a topic goes to that team's lead.

## Decisions needed

1. Routing as above (reply = exact, free text = current addressee shown in every answer, ask with buttons when
   unclear) — or always require a reply / `@name` and refuse plain text?
2. Who receives escalations: owners only (as today) or owners and admins?
3. The group-with-topics mode: later (as proposed) or not at all?

## Impact

`internal/notify/telegram` (bot per company, webhook, addressee state per membership), `internal/core/telegram.go`
(state per company and membership, webhook route), company secrets, web Settings → Integrations,
`docs/messaging.md`. `FACTORY_TELEGRAM_TOKEN` stays for a single-company Core.
