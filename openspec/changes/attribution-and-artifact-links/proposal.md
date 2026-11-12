Status: proposed and implemented 2026-11-12 (from two cases the owner reported); verified with unit tests and fakes.
Code refers to the two parts as approval-attribution (D1) and artifact-links (D2).

## Why

**D1. A delegate's approval reached the requester as the owner's.** A product designer requested approval of an
internal artifact. Under the default `lead` mode (approval-delegation) the team lead `ceo` decided it, yet the
designer's run got `authority_input: {author: "owner", rank: 0, text: "artifact approval … approved by the owner: …"}`.
`approvalDecided` and `artifactApproved` wrote every decision as the owner's, and the answer to the approval's
question was stored as the owner's message as well. The agent was told the owner had decided, with the owner's rank,
which wins every conflict; a task waiting for a message from the owner would also have woken on it.

**D2. A message to the owner named an artifact the owner could not find.** The lead asked the owner a question whose
body said "artifact stashy-lens-pouch-design" in plain text. The inbox item and the lead's chat showed no artifact: it
lived only in the Library and in the materials of the designer's task. Messages already accept `materials` (artifact
versions, report attachments; inbox-visual-previews), but an agent that names an artifact without attaching it leaves
the owner searching, and the inbox had no way to open an artifact's Library card.

## What Changes

- **D1 — decisions are the decider's.** The `approval_decided` trigger on the requesting task and the
  `artifact_approved` triggers on tasks that require the version are written with the actual decider as actor: the
  owner for a person's decision (unchanged: author `owner`, rank 0), the delegate agent for a delegated one (its own
  name and rank in `authority_input` and the brief). The text says `approved by <agent> (delegated: <setting>)`
  instead of "by the owner". The decision still wakes the requester from its wait or ask exactly as before. The answer
  Core writes to the approval's question is sent by the delegate, not by the owner.
- **D2 — artifacts named in a message to people go with it.** When a message to the owner (an agent's question,
  notification or review request), a question forwarded up the chain (the question and the forwarder's note) or a
  decision proposal (its text and its `artifacts`) names an artifact registered in its project, Core adds that
  version as a material: `factory://artifacts/<id>/<version>` and `<id>@<version>` name the version, `<id> <version>`
  too, a bare id means its latest version. A bare id counts only when it has `-`, `_` or a digit (a plain word such
  as "logo" that happens to be an id links nothing). Unregistered ids and versions are ignored, never refused; the
  sender's own materials come first and are not repeated; the limit of materials per message holds. Messages between
  agents are unchanged.
- **D2 — the inbox opens them.** The web shows such materials as before (inline images, material rows, the viewer)
  and adds "In the Library: <id> <version>" links and an "Open in the Library" link in the viewer; the Library takes
  `?artifact=<id>` (with `project`), lists only that artifact's card and opens it. The TUI lists them and `v` previews
  them like an approval's artifact (one per press when there are several); a message's preview decides nothing.

Not in scope: prompt edits (the tool descriptions already ask for materials), artifacts of another project, linking
report attachments named by path (the `visual_without_attachment` warning stays for those).

## Impact

`internal/storage` (triggers, approvals, messages, journal, new artifact_links.go), `internal/messaging` (Reply.Delegate,
internal only), `internal/web` (Library `artifact` filter), `internal/tui` (inbox `v`, preview), web inbox visuals,
viewer and Library page. Specs: `authority-input`, `messaging`. Related: approval-delegation (archived),
inbox-visual-previews, people-attribution.
