Status: proposed 2026-11-08 by the owner ("I want to move by milestones; the backlog seems to keep growing and I do
not see which goal we are heading for"). Nothing implemented.

## Why

The Factory has no notion of a milestone. Tasks are grouped only by OpenSpec change (`openspec_change`), and the
board, the home page and the inbox show tasks, not goals. In gnome-arena the producer keeps milestones in the game
repo (`docs/roadmap.md`: M1–M7, the friends playtest, the end of prototype 2.0), but the Factory does not read it:

- the owner cannot see the current goal, how far it is, or what blocks it, without opening the repository;
- on 2026-11-08 four milestones ran at once (M4 netcode, M5 front end, M5b talents, the Cinderforge map), and the
  roadmap was already behind (it said talents start after M5; they were in progress);
- the backlog *looks* like it keeps growing: a change is sliced into 5–10 tasks the moment it starts, while in the
  last day 51 tasks were created and about 66 closed; 25 BACKLOG tasks are the deferred 2D art (M6).

## What Changes

- **Milestones in Core.** A project has milestones: title, goal, "done when", an order, a status
  (`planned`, `active`, `done`, `deferred`) and the OpenSpec changes and tasks that belong to it. A task belongs to a
  milestone through its change, or directly. Leads (the producer) create and edit milestones through Factory tools;
  the repo roadmap may stay as the long text and link to them.
- **The owner picks what is active.** Making a milestone active, deferring it or reordering them is the owner's
  decision (a decision request from the lead, decided in the inbox or chat). By default one milestone is active per
  project; the owner may allow more.
- **Work follows the active milestone.** A lead may create tasks outside the active milestone only into BACKLOG or
  marked `off_milestone` with a one-line reason (bugs, infra, QA follow-ups). The scheduler does not change: the
  marker only makes the split visible.
- **Progress visible to the owner.** The home page and the board show each project's active milestone: goal, done /
  open / blocked task counts, what blocks it (the same reasons as task cards), the next milestone, and a rough ETA
  from recent throughput (labelled as an estimate). The board can filter by milestone; tasks show a milestone chip.
  Backlog growth is shown as created vs closed per day for the milestone, so slicing a change does not read as
  growth.
- **Closing a milestone.** When every task of the active milestone is done, the lead sends the owner a milestone
  report (what shipped, the build or commit to play, what to check, known gaps) as an inbox item; the owner accepts it
  (the milestone becomes `done`) or sends it back with notes, and picks the next active one.

Not in scope: scheduling priority by milestone (later, if the marker is not enough); dates and deadlines; cross-project
milestones; importing `docs/roadmap.md` automatically (the producer creates the milestones once by hand).

## Open questions (with recommendations)

1. **How many active milestones.** One per project by default, the owner may raise it (recommended), or none enforced.
2. **Tasks outside the active milestone.** Allowed with the `off_milestone` marker and reason (recommended), or held in
   BACKLOG until the owner approves.
3. **Milestone source of truth.** Core (recommended; the roadmap doc links to it), or the repo roadmap parsed by Core.
4. **ETA.** From the last 3 days' closed tasks of the same agents (recommended, shown as "≈"), or no ETA.

## Impact

Core: milestones table and API, task and change links, lead tools (`milestone_create`, `milestone_update`,
`milestone_report`), owner decisions for activation, the `off_milestone` marker. Web and TUI: home and board milestone
panel, board filter, task chip, milestone report in the inbox. Telegram: the milestone report. Producer prompt: keep
milestones current, ask the owner before starting the next one. New spec `milestones`. Related:
`throughput-signals`, `reports-decisions`, `task-board-ux`, `owner-web`.
