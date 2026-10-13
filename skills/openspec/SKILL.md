---
name: openspec
description: Work with a project's existing OpenSpec specifications and changes.
---

Read the relevant project specification and active change before changing behavior.
Keep proposals, design decisions, requirements and tasks consistent with the delivered code.
Mark tasks complete only when implementation and relevant verification have passed.
Do not invent an OpenSpec CLI command; inspect the installed command's help when needed.

Factory integration is file-based. Use factory_openspec_init to bootstrap missing directories without changing existing configuration, factory_openspec_list to discover current specs/changes, factory_openspec_read to read documents lazily, and factory_openspec_check to check an active change reference. Paths are relative to the project's openspec directory. Continue paged reads with next_offset and the returned sha256; restart a read if content changed.

Author documents with normal workspace tools or the installed official OpenSpec CLI. Inspect its help before using it. Factory's check verifies a change name, directory and nonempty proposal only; it does not replace OpenSpec schema/semantic validation. Custom schemas and existing files are preserved. Task openspec_change is the active change name, not a path or archived change. Create its proposal before linking tasks. Relevant specs and the current change must be read before implementing a linked task. Keep product requirements in these files; keep task status, dependencies, execution and review in Factory. A Markdown checkbox never accepts a Factory task. Do not paste entire specifications into reports or decision values.
