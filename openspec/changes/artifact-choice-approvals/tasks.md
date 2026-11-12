## 1. Core model and API

- [ ] 1.1 Approval gains `options` (artifact id, version, label, note), `recommended`, `chosen` and decision kinds `choose`, `changes`, `reject`; one option keeps the yes/no flow
- [ ] 1.2 `factory_artifact_request_approval` accepts `options` and `recommended`; Core validates every option against registered versions of the project (unknown, duplicate, fewer than 2 or more than 6 refused with a hint)
- [ ] 1.3 Owner decide API accepts `choice` (option), `changes` (text, optional options), `reject` (reason); compare-and-set as today
- [ ] 1.4 Choosing marks the chosen version approved and the others not chosen; tasks requiring an approved version accept only the chosen one
- [ ] 1.5 `approval_decided` input names the decision, the chosen option or the requested changes, and the real decider

## 2. Delegation

- [ ] 2.1 `factory_approval_decide` accepts `choice`/`changes`/`reject` for choice approvals; a delegate choice requires cites of the owner's answers or decisions
- [ ] 2.2 A delegate cannot approve a choice approval without choosing; it escalates to people instead
- [ ] 2.3 People-only mode refuses every agent decision on a choice approval

## 3. Owner surfaces

- [ ] 3.1 TUI inbox: options as a list with label, note and recommendation; `v` previews the focused option; enter chooses; `c` asks for changes; `n` rejects all
- [ ] 3.2 Web inbox, task page and lead chat card: options with previews (images inline, prototypes open in a new tab), choose / ask for changes / reject
- [ ] 3.3 Telegram: one button per option plus "Changes…" and "Reject"
- [ ] 3.4 Library: a choice approval links each option's card and shows which one was chosen

## 4. Tests and docs

- [ ] 4.1 Storage and Core tests: request validation, owner choose/changes/reject, delegate with and without cites, people-only refusal, required-version gating
- [ ] 4.2 TUI and web tests with fakes (no live runs)
- [ ] 4.3 Spec deltas merged into `openspec/specs` on archive
