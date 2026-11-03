## Why
The node kept the files it materializes into a task worktree (the OpenSpec tree, `.factory/`, pinned Claude skills under `.claude/skills/<name>/`) out of git by appending rules to the repository's `info/exclude`. git reads that file from the repository's common directory, so the rules applied to the main checkout and every linked worktree of the same repository. Observed: `/openspec/` landed in the owner's main checkout `.git/info/exclude`, and new files the owner added under `openspec/` were silently ignored by `git add` (tracked files were unaffected).

## What Changes
- The node's ignore rules apply only to the worktree the files were materialized into. They live in `<worktree git dir>/factory-exclude`, which the worktree's own configuration names as `core.excludesFile` (`extensions.worktreeConfig` + `git config --worktree`). git honours this per worktree; the main checkout and sibling worktrees do not see it. In a plain clone (the main worktree itself) the same mechanism applies to that checkout.
- Because `core.excludesFile` replaces the user's global excludes file for that worktree, `factory-exclude` also carries a copy of it (refreshed on each write), so global ignores keep working there.
- Enabling `extensions.worktreeConfig` moves a `core.bare=true` / `core.worktree` of the common config into the main `config.worktree`, as git requires.
- Cleanup: when the node writes a rule, the exact same line left in the shared `info/exclude` by earlier versions (`/<openspec dir>/`, `/.factory/`, `/.claude/skills/<name>/`) is removed; every other line stays.
- The factory's own bare clone no longer gets `/.factory/` in its shared `info/exclude`; each task worktree excludes it at ensure and before every seal.
- Fix: the Claude skills exclude computed a wrong anchor for a relative cwd under a symlinked temp/working directory (Abs now precedes EvalSymlinks).

## Capabilities
### New Capabilities
- `per-worktree-git-excludes`: the node's git ignore rules for materialized files are scoped to the factory's worktree.

## Dependencies
`core-owned-project-data` (materialization), `task-worktrees` (`.factory/`), `agent-config` / `runtime-drivers` (Claude skills).
