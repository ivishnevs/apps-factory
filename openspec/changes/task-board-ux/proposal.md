Status: proposed 2026-11-06 by the owner (open a task in a modal, review the board's UX); review findings below,
nothing implemented. Decisions marked "owner" are open.

## Why

The board is where the owner follows the team, and with a real project (Gnome Arena: 140+ tasks, 6 slots) it gets
in the way:

- **Opening a task leaves the board.** A card links to the full page `/tasks/{id}`: the board unmounts, the task
  page shows skeletons until its own fetch answers (seconds on the sandbox), and going back loses the scroll
  position of each column and which folded columns (Backlog, Done) were open (that state is not in the URL).
  Looking through several tasks is open → back → find the place again.
- **Wrong reason on queued tasks.** Ready tasks of gameplay-dev say "All nodes busy · Starts when a node frees up"
  while the node has free slots: the agent is at its own limit (`max_concurrent_runs: 2`). Core sends reason
  `capacity` with clearer "wait for the agent's current run" and no detail text; the web only recognises the
  agent case from a detail string ("agent X is busy …") and falls back to "All nodes busy".
- **Two words for one state.** A card sits in the "Waiting" column while its page shows the badge "ready": the
  board places a task by its reason (`columnOf`: READY with reason `dependencies` → Waiting), while the task page's
  header shows the raw engine status (`StatusBadge value={task.status}`, READY). Every one of the 41 tasks waiting
  for dependencies in Gnome Arena opens with "ready" on top. The owner sees two answers to "where is this task".
- **Waiting mixes "comes in its turn" with "stuck".** The column holds both tasks that will start on their own
  (dependencies, timers, artifacts on the way) and tasks that need someone to act (a decision of yours, an
  escalation, a failed run to inspect, a BLOCKED task, no connector, no assignee, a pause). Only the owner's ones
  get an amber card; the other stuck ones look like the queue.
- **Priority marks say nothing.** Priorities in use are 0–60 (most 30–50); the mark assumes 0–10 (8+ = high), so
  nearly every card shows three orange bars, and the page shows "P45" without a scale.
- **Waiting is a wall.** 42 of 50 open tasks are in Waiting, almost all on dependencies, sorted by priority: the few
  that wait on a person or a failure drown among those that simply wait their turn in a chain.
- **Running cards say only "Running".** No run time, no current step, no node, though the overview has them.
- **Small things.** The task page's main action is truncated ("Direct…"); there is no way to go to the next or
  previous task without going back; no keyboard navigation on the board.

## What Changes (proposed)

- **Task opens over the board.** Clicking a card opens the task in a large side panel (desktop) or a full-screen
  sheet (phone) over the board; the URL becomes `/tasks/{id}` (shareable, reload opens the full page), Back or Esc
  closes it and the board is exactly as it was (scroll, folded columns, filters). The panel shows what the page
  shows (status and who it waits on, actions, description, submission, materials, comments, details, attempts,
  timeline) and has "Open full page" plus previous/next within the current column. Board state (open columns,
  view) moves into the URL or session so it survives. Owner: side panel or centred modal — side panel recommended
  (the board stays visible, cards can be switched while it is open).
- **Instant open.** The panel renders at once from the board's data (title, status, why, assignee) and fills the
  rest when the task's details arrive; details of hovered or neighbouring cards are prefetched.
- **One vocabulary.** The column name is the status the owner sees everywhere (card, panel, page, Home, Telegram):
  Backlog, Blocked, Waiting, Ready, Running, In review, Done. The task header's badge shows that column (today it
  shows the engine status: "ready" on waiting tasks); the engine status (READY, REWORK, BLOCKED) appears only as a
  detail where it adds something.
- **Correct "why" for capacity.** Core sends the capacity case structured (agent limit with the agent and its
  running tasks, project writer, node slots, unpushed work on a node) instead of a free-text detail; the web says
  "gameplay-dev runs 2 of 2 · starts when one ends" with links to the running tasks. "All nodes busy" only when the
  nodes are full.
- **Blocked column (owner 2026-11-06).** A new column between Backlog and Waiting for tasks that will not move
  without someone acting: waiting on you or another person (decision, approval, answer, review by people), escalated,
  parked for inspection, a failed run awaiting a decision, status BLOCKED, no connector can run it or its connector is
  offline, no assignee, paused. Each card says who has to act and what to do; yours stay amber and first. Waiting
  keeps only what comes in its turn on its own: dependencies, artifacts being produced, timer waits, questions to
  other agents. The phone status bar and Home use the same split; Blocked is never folded when it has tasks.
- **Waiting that reads.** Within Waiting, dependency waits name the task they wait on with its column, and can be
  collapsed by chain or blocker (owner: group or filter).
- **Priority with a scale.** Either show the number with its rank in the project (top / middle / low thirds) or
  normalise priorities to a fixed scale (owner).
- **Running cards with progress.** Run time, last step and node on running cards.
- **Board keyboard and navigation.** j/k or arrows between cards, Enter opens, Esc closes; previous/next in the
  panel.
- **Fixes.** Untruncated action labels in the task header; a UX pass over the board and task view on phone and
  desktop against the owner-web checklist (contrast, focus, touch targets, empty and loading states).

## Impact

Web: tasks board and task view (`web/src/app/c/[company]/tasks`, `components/app/tasks`, `lib/humanize`), routing
(intercepted route for the panel). Core: structured capacity reason in the task verdict and overview. Spec:
`owner-web` (board and task view). Related: `human-review-visibility` (who a review waits on), `team-page-clarity`.
