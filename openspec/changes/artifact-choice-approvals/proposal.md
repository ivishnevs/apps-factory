Status: proposed 2026-11-12 by the owner ("can we extend approvals beyond yes/no?"), approved by the owner the same day with two additions: a delegate may drop weak options and escalate the rest, and a delegate choice may cite people-written company or project rules.
Implemented 2026-11-12. Implementation decisions: options are numbered from 1; `recommended`, `choice`, `options`
and `drop[].option` take the number, the label or `id@version` (an artifact id when only one option has it); labels
are required among several options, one line up to 60 characters, unique per approval (notes one line up to 300).
Every artifact approval is options (see "One approval model" below and its implementation decisions); zero options,
more than 6, or `options` together with `artifact_id` are refused. A version already pending or approved refuses the choice (409, naming
the option); a not chosen option may be asked for again. The versions a choice offers live in a new table
`approval_options` (migration 0043); on choosing, the approval's `artifact` and artifact columns become the chosen
version, so gating, retention and Library lookups treat it as an approved version and its siblings as `NOT_CHOSEN`;
pending options are kept from retention. Asking for changes ends the approval as `CHANGES_REQUESTED` and, from the
owner, is a board comment on the task like a rejection. A choice is not an artifact version line in people topics:
it joins its task's topic; a new choice supersedes pending yes/no requests of other versions of its artifacts.
Delegate cites: a 32-hex message id people wrote (or a question people answered), an approved decision id, or
`company rules: <quote>` / `project rules: <quote>` whose quote is in those rules (case and spacing ignored); since
the one approval model the check applies to every artifact approval. Under the people-only mode an agent reached by
strict delegation may only escalate. Telegram: one button per option plus "Changes…" and "Reject"; a reply asks for
changes, a reply starting with "reject" rejects all with the rest as the reason. `factory_artifact_get` adds
`version_status` for an option.

## One approval model (added 2026-11-12)

After the first implementation the owner pointed out that a choice approval and a regular approval are the same
thing. So there is one model: every artifact approval is a set of 1–6 options. A request for one version is an
approval with one option; approving it is choosing that option. Decisions (choose, ask for changes, reject all),
delegate rules (a delegate approves only with checked cites of what people said; changes and reject need a reason)
and owner surfaces are the same whatever the number of options. This also closes the gap where yes/no approvals in
`lead` mode only checked that cites were non-empty, and gives single-version approvals "ask for changes".

Implemented 2026-11-13. Implementation decisions: there is one request path (`RequestArtifactApproval`) and one
decision path (`decideChoice`) for every artifact approval; the yes/no artifact path is removed. `artifact_id` /
`artifact_version` stay as the request's shorthand for one option (its label may be empty; people and agents then
see `id@version`). A one-option approval has its `artifact` and version columns set from the request (the only
candidate), plus its `approval_options` row like any other, so required-version gating, retention, composites and
people-topic version lines treat it as before; with several options they stay empty until one is chosen. A
one-version approval is still an artifact version line in people topics; several options join the task's topic.
`approve` is accepted on a one-option approval as choosing option 1 (owner API, CLI `factory approve`, topics,
Telegram, delegates), and refused on several options; topics list `choose`/`changes`/`reject` for every artifact
approval and the web's "Approve all" chooses each single-version approval's only option. Choosing takes an optional
comment for one option too. The delegate rules are one code path: approving (choosing) needs cites checked by
`checkCites` in `agent` and `lead` mode alike (before, a yes/no approval in `agent` mode needed no cite at all);
changes and reject need a reason and no cite; `drop` is refused on one option (escalate it whole). The requester's
text for one option keeps the familiar form ("artifact approval … of lens@2 approved by the owner: …", "Approval …
CHANGES_REQUESTED: …"). Telegram: one version shows Approve, Changes… and Reject, and a reply now asks for changes
("reject: <reason>" rejects) instead of rejecting (confirmed by the owner 2026-11-13). Tool-call approvals (`factory_owner_request_approval`, command
guards) are a different kind and stay approve/reject; they are never delegated. No migration: the schema already
had `approval_options` and the artifact columns.

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
  The request may name the agent's recommendation. A request for one version is an approval with one option (see
  "One approval model").
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
