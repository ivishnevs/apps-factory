## 1. Core model and API

- [x] 1.1 Approval gains `options` (artifact id, version, label, note), `recommended`, `chosen` and decision kinds `choose`, `changes`, `reject`; one option keeps the yes/no flow
- [x] 1.2 `factory_artifact_request_approval` accepts `options` and `recommended`; Core validates every option against registered versions of the project (unknown, duplicate, fewer than 2 or more than 6 refused with a hint)
- [x] 1.3 Owner decide API accepts `choice` (option), `changes` (text, optional options), `reject` (reason); compare-and-set as today
- [x] 1.4 Choosing marks the chosen version approved and the others not chosen; tasks requiring an approved version accept only the chosen one
- [x] 1.5 `approval_decided` input names the decision, the chosen option or the requested changes, and the real decider

## 2. Delegation

- [x] 2.1 `factory_approval_decide` accepts `choice`/`changes`/`reject`/`escalate` for choice approvals; a delegate choice requires cites, and Core checks each cite names a people's answer, a recorded decision, or a people-written company or project rule
- [x] 2.2 A delegate cannot approve a choice approval without choosing; changes and reject need a reason, no cite
- [x] 2.3 `escalate` accepts `drop: [{option, reason}]` keeping at least one option; dropped options are stored with the reason and stay choosable by people
- [x] 2.4 People-only mode refuses every agent decision on a choice approval

## 3. Owner surfaces

- [x] 3.1 TUI inbox: options as a list with label, note and recommendation, dropped options collapsed with the delegate's reasons; `v` previews the focused option; enter chooses; `c` asks for changes; `n` rejects all
- [x] 3.2 Web inbox, task page and lead chat card: options with previews (images inline, prototypes open in a new tab), choose / ask for changes / reject
- [x] 3.3 Telegram: one button per option plus "Changes…" and "Reject"
- [x] 3.4 Library: a choice approval links each option's card and shows which one was chosen

## 4. Tests and docs

- [x] 4.1 Storage and Core tests: request validation, owner choose/changes/reject, delegate with and without cites, people-only refusal, required-version gating
- [x] 4.2 TUI and web tests with fakes (no live runs)
- [ ] 4.3 Spec deltas merged into `openspec/specs` on archive

## 5. One approval model (owner, 2026-11-12: "a choice approval and a regular approval are the same thing")

- [x] 5.1 Every artifact approval is options 1–6: a single-version request is stored and served as one option, not converted into a separate yes/no kind; remove the separate yes/no path
- [x] 5.2 Decisions are choose / changes / reject for every approval; owner "approve" on one option is choose; "ask for changes" exists for single-version approvals too
- [x] 5.3 Delegate rules are the same for every approval: approving (choosing) needs cites checked by `checkCites`; changes and reject need a reason and no cite; no approve without choosing
- [x] 5.4 Owner surfaces (TUI, web, Telegram, Library) use the one choice card; one option renders compactly with Approve / Ask for changes / Reject
- [x] 5.5 Tests: single-version approval through the choice path (owner approve, changes, reject; delegate approve with and without valid cites)
