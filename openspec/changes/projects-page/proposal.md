Status: requested 2026-11-07 by the owner (Projects in the menu); ready to design and implement. Nothing implemented.

## Why

Projects are the unit the work is organised by (tasks, repositories, rules, and soon their own direction, approval
settings and context), but the web has no place for them. The menu has Home, Tasks, Inbox, Chat, Activity, Library,
Costs, Teams & agents, Nodes, Skills, Secrets and Settings (`web/src/components/app/shell.tsx`); a project is created
from a button on the Tasks page and edited in a sheet inside Settings (name, description, repository, branch, team).
There is no list of projects with their state and no page of a project.

## What Changes

- **Projects in the menu**, next to Tasks. The list shows each project: team, repository, setup state, open / blocked /
  in progress / in review counts, what needs you, last activity, cost this month. "New project" moves here (Tasks
  keeps a shortcut).
- **A project page** (`/projects/{id}`):
  - overview: what is happening, what needs you, the latest results and merged pull requests;
  - goal and direction of the project and its rules (`lead-brainstorm-mode`: the lead judges "fits the direction"
    against them);
  - settings: name, description, repository, branch, team (moved from Settings), approval modes per kind
    (`approval-delegation`), archive;
  - links to its board (Tasks filtered by the project), chat with its lead, library and costs.
- **Settings** keeps company-level things only; its project section links to the project pages.
- Phone: Projects in "More", the project page works as a single column.

## Impact

Web: navigation (`shell.tsx`), new Projects list and project page, Settings project section, links from Home, Tasks
and Chat. Core: a project summary route if the overview needs counts not in the existing overview. Spec: `owner-web`.
Related: `lead-brainstorm-mode` (project direction), `approval-delegation` (per-project modes), `structured-catalog`
(project settings in tables), `task-board-ux` (column names in the counts).

## Implementation (2026-11-07)

Projects is in the menu after Tasks (and under More on phones): a card per project with its team, repository, setup,
tasks by column, what needs you, running runs, last activity and the cost of its ended runs; New project is here;
archived projects fold at the end. The project page has the overview, Needs you, Recently done, Goal and direction
and Rules (saved on the project, carried by every brief), Settings (moved from Settings) and Archive / Restore, with
links to its board, the chat with its team's lead, its library and costs. Settings → Projects lists the projects and
links to their pages. Decided during implementation: no new Core summary route (the overview has everything); the
project's cost is the sum of its tasks' ended runs; archiving refuses a project with tasks in work (backlog tasks
may stay) and the board's project filter hides archived projects. Approval modes join the page with
`approval-delegation`.
