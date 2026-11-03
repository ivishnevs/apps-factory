## ADDED Requirements

### Requirement: Materialized files are ignored only in the factory's worktree
The node SHALL keep the files it materializes into a worktree (the OpenSpec directory, `.factory/`, pinned skills under `.claude/skills/<name>/`) out of `git status` and `git add -A` in that worktree, and SHALL NOT add rules that apply to the main checkout or other worktrees of the same repository. It SHALL NOT write these rules to the repository's shared `info/exclude`. The user's global excludes file SHALL keep applying in the factory's worktree.

#### Scenario: Owner's checkout unaffected by a task worktree
- **WHEN** the node materializes `openspec/` and `.factory/` into a task worktree created from the owner's repository
- **THEN** `git add -A` in the task worktree SHALL stage neither directory
- **AND** a new file the owner creates under `openspec/` in the main checkout SHALL appear as untracked there

#### Scenario: Plain clone
- **WHEN** the workspace is a plain clone (no linked worktree)
- **THEN** the materialized files SHALL be ignored in that clone as before

### Requirement: Rules left by earlier versions are removed
When the node writes an ignore rule for a worktree, it SHALL remove the identical line from the repository's shared `info/exclude` and SHALL leave every other line there unchanged.

#### Scenario: Legacy `/openspec/` line
- **WHEN** the shared `info/exclude` holds the user's `*.tmp` and the node's earlier `/openspec/` and `/.factory/`
- **THEN** after the next materialization it SHALL hold only `*.tmp`
