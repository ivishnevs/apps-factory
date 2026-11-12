Status: proposed 2026-11-12 by the owner ("can we extend approvals beyond yes/no?"), approved by the owner the same day with two additions: a delegate may drop weak options and escalate the rest, and a delegate choice may cite people-written company or project rules.

## Why

Seen on 2026-11-12 in tvorog, project stashy, twice in one day.

1. product-designer finished a prototype of the hybrid A+C and requested approval of `stashy-lens-pouch-design@1`
   (audience internal). Under the default `lead` mode the CEO decided it: "accepted as material to show; the design
   decision is the owner's". Five seconds later the CEO asked the owner a separate question with options (icon A or B,
   AI voice, paper cards or AI covers).
2. product-designer made three variations (V1 "Mascot", V2 "Editorial", V3 dark) and requested approval of
   `stashy-variations-compare@2`. The CEO again approved it "as material to show; the choice is the owner's" and asked
   the owner a question with buttons V1 / V2 / V3.

What went wrong is the shape of the approval, not the agents:

- **An approval is yes/no on one artifact version.** What the team needed from the owner was a choice among several
  versions. The approval could not carry it, so it was decided as a formality and the real decision moved to a
  question.
- **The delegate's approval said nothing.** "Approved as material, the choice is the owner's" is neither a decision
  nor an escalation. It passed the lead gate without the lead deciding anything.
- **The question lost the artifacts.** The owner's question had the options as text and no artifacts, so the owner
  could not see what they were choosing between from the Inbox or the task (`attribution-and-artifact-links`
  adds links; a choice approval makes them the options themselves).

## What Changes

- **Choice approvals.** An agent can request one approval over 2–6 options. Each option is an artifact version
  already registered in the project (`id@version`) with a short label ("V1 Mascot") and an optional one-line note.
  The request may name the agent's recommendation. A request with one option stays the existing yes/no approval.
- **Decisions on a choice.** The decider either **chooses** one option (optional comment), **asks for changes**
  (required text: what to change, optionally on which options), or **rejects all** (required reason). Choosing
  records the chosen version as approved and the others as not chosen (not rejected: they stay usable later).
- **The same delegation rules.** `internal_artifacts` / `external_artifacts` modes apply to choice approvals as they
  do to single ones. A delegate may choose only when what people said covers the choice — their earlier answers,
  recorded decisions, or the company's or project's rules written by people — and SHALL cite it; Core checks that
  each cite names one of those. Otherwise it escalates to people. A delegate cannot approve a choice without
  choosing. Asking for changes or rejecting all needs no cite: that is the delegate's own judgement (its role and the
  rules), with a required reason.
- **A delegate may filter before escalating.** It may drop options it judges not good enough, each with a reason,
  and escalate the rest (at least one). People see the dropped options collapsed with the delegate's reasons and can
  still choose one of them. Dropping every option is "reject all".
- **What the requester gets.** The `approval_decided` input on the requesting task names the decision, the chosen
  option (`id@version` and label) or the requested changes, and who decided (owner or the delegate, per
  `attribution-and-artifact-links`).
- **Owner surfaces show the options.** Inbox (TUI and web), the task page, the lead chat card and Telegram show a
  choice approval as its options: label, note, recommendation mark, and a preview of each version (TUI `v` previews
  the focused option; the web shows images and opens prototypes). Answering anywhere closes it everywhere (same
  compare-and-set as today).
- **Tools.** `factory_artifact_request_approval` accepts `options: [{artifact_id, artifact_version, label, note?}]`
  and `recommended` (an option index or artifact id). Core validates every option against the project's registered
  versions and refuses unknown or duplicate ones. `factory_approval_decide` (delegates) accepts `choice`,
  `changes` or `reject`, and `escalate` with optional `drop: [{option, reason}]`.
- **Tasks that require an approved version** keep working: the chosen version is an approved version; the not-chosen
  ones are not.

Not in scope: choices that are not artifacts (a question with options stays `factory_owner_ask`), ranking, partial
merges of options (that is "ask for changes" with text).

## Impact

- Specs: `artifact-approvals` (choice approvals, decisions, delegation), `factory-mcp` (tool arguments),
  `owner-web`, `owner-ui` (inbox and task rendering), `messaging` (Telegram buttons), `authority-input`
  (what the requester receives).
- Code: `internal/approvals` (options on the approval, decision kinds), `internal/storage` (store options and the
  chosen one; approval_decided text), `internal/core` (request validation, delegate decide rules),
  `internal/factorytools` (schemas), `internal/tui` inbox/links, `internal/web` inbox, task page and chat cards,
  Telegram notifier.
- Data: no migration (unreleased); the approval row gains options and the chosen option.
