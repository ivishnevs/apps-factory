Status: problem recorded 2026-11-05 by the owner (a blocker in practice); owner decided 2026-11-06: a hard gate,
every task outside the backlog has a reviewer. Nothing implemented.

## Why

A team can have roles that no review rule covers. In the game team (`gnome-forge`) the review policy covered
developers (reviewed by QA) and the artist (reviewed by the game designer), but not the game designer, QA or the
lead. Their submitted work went to review with no reviewer, Core resolved it to "people", the board showed an
empty reviewer, and the task sat in review with nobody visibly responsible (twice on 2026-11-05). The catalog
accepted this configuration silently. (Fixed by hand for that team with a rule "the producer reviews design and
QA work".)

## What Changes (proposed)

- **Every agent resolves to a reviewer.** Catalog validation checks that each agent of a team either is covered by
  a review rule naming an agent reviewer, or explicitly reviews to people (`reviewer: owner` or a named person).
  An agent with neither is refused (or, at least, saved with a visible warning on the team page and in the save
  result) — to decide.
- **A default for the uncovered.** Team presets and the team wizard add a rule "the lead reviews every role
  without its own reviewer"; the lead's own work is reviewed by people, explicitly.
- **Explicit, never empty.** The reviewer of a submitted task is always a name: an agent, or "Owner" / the person
  (`human-review-visibility`). Core never stores an empty reviewer for a task in review.
- **Show the mapping.** The team page lists who reviews whom (`team-page-clarity`).

## Impact

Catalog loader validation (review coverage per agent), team presets and wizard, Core review resolution, web team
page and board. Related: `human-review-visibility`, `team-page-clarity`, `people-attribution`.

## Owner decision (2026-11-06): a hard gate on the task

- **A reviewer is required on every task except in the backlog.** A task may lack a reviewer only while it is in
  BACKLOG; it cannot be made ready (To do) without one, and no later status may lose it.
- **Agents set it.** An agent that creates a task straight into ready or moves a task to ready must set the reviewer
  (an agent or a person: the owner or a named member); Core refuses the call otherwise with a clear error naming the
  missing reviewer. The review policy may suggest the reviewer (the tool's error and the brief say whom the policy
  would choose), but the task carries it explicitly.
- **People get the same error.** In the web and TUI, making a task ready without a reviewer is refused with "Reviewer
  is required"; the new-task form and the task page have a reviewer field (prefilled from the policy's suggestion).
- **Catalog validation stays as the second line:** agents not covered by a rule are warned about in the catalog, so
  the suggestion exists, but the gate is on the task.
- Existing open tasks without a reviewer: a one-time fill from the policy where it resolves one; the rest are listed
  to the owner.
