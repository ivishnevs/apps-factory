Status: accepted 2026-11-06 by the owner (configurable; the lead decides from earlier answers; a hard gate can be
switched on); to design and implement. Nothing implemented.

## Why

Today only a person (owner or admin) can approve an artifact version, and decisions need the owner unless a category
is explicitly delegated in the policy. On the game project the owner became the bottleneck for internal material:
artist-3d could not start the Rumbler until the owner approved artist-2d's concept pack, while the game designer was
already reviewing that art and the owner had no more to add. The owner also answered questions in the chat whose
consequences then came back as separate approvals.

## What Changes

One setting for **who approves**, for artifacts and for decisions, on a team or a project (the project overrides the
team), with three modes:

- **Owner (hard gate).** Only people approve. Switched on explicitly where the owner wants to sign personally (store
  material, external publications, anything costing money); never relaxed by an agent.
- **Delegated.** A named agent approves (for example the game designer for the team's internal art). It escalates to
  people when unsure (style, scope, conflict with a recorded decision); its approvals are recorded as made by it under
  the delegation.
- **Lead decides from earlier answers.** The lead approves itself when the owner's earlier answers and recorded
  decisions already cover it (it cites them); anything new or not covered goes to people. This is the default for
  internal work.

Kinds of thing the setting applies to: internal artifacts (concepts, references, textures), external artifacts
(store, publication), decision categories (`decision-approval-ux`), each with its own mode; spending and destructive
changes always stay with people. The team and project pages show the modes in plain words ("Internal art: approved by
game-designer, escalates when unsure"), editable by owners. Every approval records who approved and under which
setting.

For the game team now (no product change needed, prompts only, offered earlier): art passes through pull requests
reviewed by the game designer rather than owner-approved artifacts; the designer escalates to the owner when unsure;
the producer asks the owner to approve only what the owner signs personally.

## Impact

Core: approval modes per team/project and kind, agent approvals under delegation with provenance, escalation path;
artifact approvals and decisions (`decision-autonomy` delegation) use the same setting. Catalog/configuration
(`structured-catalog`), team and project pages, Inbox (only what reaches people). Specs: `artifact-approvals`,
`decision-autonomy`. Supersedes task 2.3 of `decision-approval-ux`.
