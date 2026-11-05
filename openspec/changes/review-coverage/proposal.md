Status: problem recorded 2026-11-05 by the owner (a blocker in practice); to be designed. Nothing implemented.

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
