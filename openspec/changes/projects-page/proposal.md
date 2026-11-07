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
