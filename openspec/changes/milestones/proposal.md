Status: proposed 2026-11-08 by the owner ("I want to move by milestones; the backlog seems to keep growing and I do
not see which goal we are heading for"). Nothing implemented. Open for discussion: the nine decisions below each
have options and a recommendation; the owner has not accepted them yet (2026-11-13). Do not implement before that.

## Why

The Factory has no notion of a milestone. Tasks are grouped only by OpenSpec change (`openspec_change`), and the
board, the home page and the inbox show tasks, not goals. In gnome-arena the producer keeps milestones in the game
repo (`docs/roadmap.md`: M1–M7, the friends playtest, the end of prototype 2.0), but the Factory does not read it:

- the owner cannot see the current goal, how far it is, or what blocks it, without opening the repository;
- on 2026-11-08 four milestones ran at once (M4 netcode, M5 front end, M5b talents, the Cinderforge map), and the
  roadmap was already behind (it said talents start after M5; they were in progress);
- the backlog *looks* like it keeps growing: a change is sliced into 5–10 tasks the moment it starts, while in the
  last day 51 tasks were created and about 66 closed; 25 BACKLOG tasks are the deferred 2D art (M6).

What the owner wants from it, in order: (1) see the one goal a project is heading for and how far it is; (2) stop
agents from opening new fronts without the owner saying so; (3) get a clear "it is done, here is what to play"
moment; (4) all of it without extra work in projects that do not need milestones (small support projects).

## Context: what exists today

- A task carries `openspec_change` (in its body); Core validates the name and reports the change's state in the done
  facts (`internal/storage/openspec.go`, `openspec_changes.go`). A change is the natural unit of a milestone's scope.
- Leads create tasks through Factory tools; Core already refuses or adjusts a task on create (assign scopes,
  duplicate warnings), so a "Core fills the milestone" rule fits the same place.
- Owner decisions with options exist (artifact-choice-approvals, `internal/core/choice_approvals.go`), as do task
  alerts and the "with <manager>" mark (inbox-badge-parity). Activation and the milestone report reuse them.
- throughput-signals (implemented, migration 0037) already counts queued work and blockers per project; the
  milestone panel reuses its blocker reasons instead of computing its own.
- Latest migration on main is 0043; this change would take the next free number at implementation time.

## What Changes (with the recommended options below)

- **Milestones in Core.** A project has milestones: title, goal, "done when", an order, a status
  (`planned`, `active`, `done`, `deferred`) and the OpenSpec changes and tasks that belong to it. Every project has a
  built-in "Ongoing" milestone that never closes and is hidden while it is the only one. Leads (the producer) create
  and edit milestones through Factory tools; the repo roadmap may stay as the long text and link to them.
- **Every task has a milestone; Core fills it in.** A task gets the milestone of its change, else the one it was
  created in, else the active milestone, else "Ongoing". An agent cannot forget, and the owner never has to pick.
- **The owner picks what is active.** Making a milestone active, deferring it or reordering them is the owner's
  decision (a decision request from the lead, decided in the inbox or chat). One milestone is active per project by
  default; the owner may raise the limit per project.
- **Work outside the active milestone is explicit.** While a milestone is active, a lead may put a task elsewhere
  (Ongoing, a planned milestone) only with `off_milestone` and a one-line reason (bugs, infra, QA follow-ups). The
  scheduler does not change: the marker only makes the split visible.
- **Progress visible to the owner.** The home page and the board show each project's active milestone: goal, done /
  open / blocked task counts, what blocks it (the same reasons as task cards), scope added since activation, the next
  milestone, and a rough ETA labelled "≈". The board filters by milestone; tasks show a milestone chip. Created vs
  closed per day is shown for the milestone, so slicing a change does not read as growth.
- **Closing a milestone.** When every task of the active milestone is done, Core tells the lead; the lead sends the
  owner a milestone report (what shipped, the build or commit to play, what to check, known gaps) as an inbox item;
  the owner accepts it (the milestone becomes `done`) or sends it back with notes, and picks the next active one.

Not in scope: scheduling priority by milestone (later, if the marker is not enough); dates and deadlines; cross-project
milestones; importing `docs/roadmap.md` automatically (the producer creates the milestones once by hand).

## Decisions for the owner (options, recommendation, risks)

History: 2026-11-08 four open questions; 2026-11-12 the owner asked whether scope can grow (yes: tasks join or leave
a milestone any time, the card shows "scope +N since start" and the ETA moves), whether a project can ignore
milestones (yes, see D1), and whether there should be a default milestone (D1 option C), then asked for the full
picture below.

### D1. Mandatory or optional

| Option | What it means | Risk |
|---|---|---|
| A. Optional, the agent chooses | Task has a milestone only if the agent sets one | Agents forget; the panel undercounts; drifts back to today |
| B. Mandatory, visible default in every project | Agents must pick; every form shows a milestone field | Noise in support projects; agents pick wrong to get past the field |
| **C. Every task has one, Core fills it (recommended)** | Built-in hidden "Ongoing"; Core assigns per the rule above | Hidden default can surprise ("why is this in Ongoing?"): the chip and the tool reply say where a task went and why |

### D2. How a task joins a milestone

Options: through its change only; directly only; **both, the direct link wins (recommended)**. A change belongs to
at most one milestone; its tasks follow it unless set directly. Risk: a change spanning two milestones; the answer is
to split the change or set the odd tasks directly, and the milestone page lists such tasks.

### D3. How many active milestones

Options: exactly one, hard; **one by default, the owner raises it per project (recommended)**; no limit. With two
active, a task without a change or a direct link goes to the first active by order, and the tool reply names it so
the lead can move it. Risk: the limit raised once and forgotten brings back four parallel fronts; the home card shows
"2 active" plainly.

### D4. Who activates, defers, reorders

Options: **the owner, through a decision request from the lead (recommended)**; the lead itself; automatically by
order when one closes. Creating, editing and planning milestones stay free for leads. Risk: the owner becomes a
bottleneck between milestones; mitigated because the report and the "next one" choice arrive as one inbox item, and
work continues in Ongoing meanwhile.

### D5. Work outside the active milestone

Options: **allowed with `off_milestone` and a reason (recommended)**; held in BACKLOG until the owner approves; free.
The marker exists only while a milestone is active. Risk: agents mark everything off-milestone; the panel shows the
off-milestone count per day, and throughput-style notice can flag it later if it happens.

### D6. Source of truth

Options: **Core (recommended; the roadmap doc links to it)**; the repo roadmap parsed by Core; two-way sync. Risk:
the repo doc goes stale; the producer prompt says Core is authoritative and the doc carries only long text.

### D7. ETA

Options: **"≈" from the last 3 days' closed tasks of the milestone (recommended)**, hidden until at least 5 tasks
closed; per-task estimates from agents; no ETA. Risk: noisy and over-trusted; always labelled as an estimate, and the
scope growth is shown next to it so a moving ETA is explained.

### D8. Closing

Options: **Core notices all tasks done and tells the lead, the lead sends the report, the owner accepts or sends back
(recommended)**; auto `done` when tasks are done; the owner closes by hand. Risk: an empty or thin report; the report
tool requires the four parts (shipped, how to play, what to check, known gaps).

### D9. Scheduler priority by milestone

Options: **not now (recommended)**; active-milestone tasks first. Risk of "not now": off-milestone work can still take
slots; revisit after a week with the marker and the off-milestone count.

## Size and risks of the change

Comparable to throughput-signals (67 files, about 1800 lines added):

| Part | Work |
|---|---|
| Storage | one migration: `milestones` table, task and change links, built-in Ongoing per project (backfill for existing projects and every new one) |
| Core | API, the fill rule on task create/update, `off_milestone` check, activation through owner decisions, progress view (counts, blockers from throughput-signals, created vs closed, ETA), "all done" notice to the lead |
| Agent tools | `milestone_create`, `milestone_update`, `milestone_report`; milestone and `off_milestone` on task tools |
| Web | home and board panel, board filter, task chip, milestone page, report card in the inbox |
| Telegram, TUI | the report with accept / send back; a milestone line on the project view |
| Prompts | producer and lead: keep milestones current, report, ask before the next one |
| Tests | fill rule across configurations (no milestones, one active, two active, change linked, direct), activation, report, ETA |

Overall risks: the fill rule touches every task create path (tools, web, Telegram, presets), so it must live in one
Core function; existing projects get a backfill into Ongoing, which must not show anything new until a real
milestone exists; companies with several projects and teams with several leads need per-project rules, never per
company.

## Impact

Core: milestones table and API, task and change links, lead tools (`milestone_create`, `milestone_update`,
`milestone_report`), owner decisions for activation, the `off_milestone` marker. Web and TUI: home and board milestone
panel, board filter, task chip, milestone report in the inbox. Telegram: the milestone report. Producer prompt: keep
milestones current, ask the owner before starting the next one. New spec `milestones`. Related:
`throughput-signals`, `reports-decisions`, `task-board-ux`, `owner-web`.
