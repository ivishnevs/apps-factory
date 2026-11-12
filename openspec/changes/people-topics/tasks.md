## 1. Brief and the gate

- [ ] 1.1 `Brief` and `ItemNote` types, `briefSchema`/`noteSchema` in `internal/factorytools`, optional `brief`/`note`
      on the tools of design D2, the shared description sentence; tool schema tests.
- [ ] 1.2 Migration 0041 (`people_topics`, `people_topic_items`, `messages.topic_id`).
- [ ] 1.3 `toPeople` gate (`internal/storage/people_gate.go`): topic lookup by key (D4), required fields per path (D3),
      `BriefError` → 400 `brief_required` with missing fields and hint; tests per row of D3.
- [ ] 1.4 Route every people-bound create and move through the gate (design "Where we are" table); architecture test
      that nothing else sets recipient owner.

## 2. Topics and items

- [ ] 2.1 Items, states, open/settled, reopen by version line within 30 days; `GET /topics`, `GET /topics/{id}` with
      Core context (D6) and per-item `actions`; overview carries topics.
- [ ] 2.2 `POST /topics/{id}/decide`: several items in one transaction (`DecideApprovalTx`, `ReplyMessageTx`).
- [ ] 2.3 `factory_approval_decide` with `approval_ids` (batch escalate, D12); new versions of a line in a people topic
      go to people with a notification to the delegate.
- [ ] 2.4 `includes` on artifact registration; a part rejected or replaced supersedes the composite (D11).
- [ ] 2.5 `factory_people_brief` (D13); `needs_brief` and timeout forwards (D14).
- [ ] 2.6 `topic` on `message_send`/`message_reply` to people, default by task; replies in a topic wake the lead (D9).

## 3. Text

- [ ] 3.1 Templates for Core's escalations and quota pause (D8).
- [ ] 3.2 Cleaned `text` for items without a brief; generated `ask`/`after` for call approvals (D7).

## 4. Surfaces

- [ ] 4.1 Web Inbox: topics (header: title, ask, recommendation, waits for you; items with notes, previews,
      previous version and your comment; buttons; "approve all"); no ids, hashes or JSON.
- [ ] 4.2 Web chat: topic card and thread in the lead's chat; replies; read state shared with the Inbox; badge parity.
- [ ] 4.3 Telegram: one message per new open item with buttons and album; updates in the digest only.
- [ ] 4.4 TUI inbox lists topics.

## 5. Migration and verify

- [ ] 5.1 Startup migration of open items (D15), idempotent; one message per lead with `needs_brief` topics.
- [ ] 5.2 Replay the 2026-11-12 cases with fakes: P3-9a key frames (escalate batch, reject three, rework v4 lands in
      the row, contact sheet withdrawn), 877fcce0 v2 next to v1, four `retries_exhausted` of one agent as one topic
      with "retry all", Nakama approval plus Q2–Q4 questions in one topic, an escalation on the second review round.
- [ ] 5.3 Docs: `docs/approvals.md`, `docs/artifact-approvals.md`, `docs/factory-mcp.md`, `docs/task-lifecycle.md`,
      a new `docs/people-topics.md`.
- [ ] 5.4 Full Go tests, vet, web tsc and lint; live check on the demo stack (web Inbox and chat in the browser).
