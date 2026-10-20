---
name: openspec
description: Work with a project's existing OpenSpec specifications and changes.
---

Read the relevant project specification and active change before changing behavior.
Keep proposals, design decisions, requirements and tasks consistent with the delivered code.
Mark tasks complete only when implementation and relevant verification have passed.
Do not invent an OpenSpec CLI command; inspect the installed command's help when needed.

Factory keeps the project's OpenSpec tree; your worktree holds a copy as of your run's start (openspec/ unless the project sets another directory, with .factory/manifest.json recording the digests). Read it as files, or the current Factory version with factory_openspec_list/read (paths relative to the openspec directory; continue paged reads with next_offset and the returned sha256, restart if content changed); factory_openspec_check checks an active change reference. Edits of those files are uploaded before your exit (factory_openspec_sync uploads earlier): if another run changed the same document meanwhile, the exit is refused, Factory's version is saved beside your file as FILE.factory-current; merge it into your file, delete the .factory-current file and exit again. factory_openspec_write/move/delete change single documents with compare-and-set on base_sha256. Never commit the openspec directory or .factory to git; they are excluded for you.

Author documents with normal file tools or the installed official OpenSpec CLI on your worktree's copy. Inspect its help before using it. Factory's check verifies a change name, directory and nonempty proposal only; it does not replace OpenSpec schema/semantic validation. Custom schemas and existing files are preserved. Task openspec_change is the active change name, not a path or archived change. Create its proposal before linking tasks. Relevant specs and the current change must be read before implementing a linked task. Keep product requirements in these files; keep task status, dependencies, execution and review in Factory. A Markdown checkbox never accepts a Factory task. Do not paste entire specifications into reports or decision values. When every task linked to a change is done, Factory creates one "Archive OpenSpec change NAME" task: check the delivered behavior against the change, run the official archive command on your worktree's copy and exit (the move below changes/archive is uploaded then).
