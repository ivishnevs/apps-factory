---
name: openspec
description: Work with a project's OpenSpec specifications and changes in its repository.
license: Apache-2.0
metadata:
  categories: planning, documentation
  eval: "Write a small OpenSpec change for adding CSV export. Good: proposal, tasks and a delta spec that validate with openspec validate --strict."
---

Read the relevant project specification and active change before changing behavior.
Keep proposals, design decisions, requirements and tasks consistent with the delivered code.
Mark tasks complete only when implementation and relevant verification have passed.
Do not invent an OpenSpec CLI command; inspect the installed command's help when needed.

OpenSpec lives in the project's repository under openspec/, like code: read it in your checkout, author it with normal file tools or the installed official OpenSpec CLI, commit spec edits on your task branch and they are reviewed with your code in the same pull request. There is no Factory copy of the specs and no OpenSpec tool in Factory MCP. Run the official validation (for example openspec validate CHANGE --strict) before you submit.

A task's openspec_change names a change (kebab-case, not a path). The node checks it on your checkout before your run starts: the change must be on the default branch (changes/NAME/proposal.md, or its archived copy under changes/archive/). A change written on an unmerged branch is invisible to other tasks, so a change is proposed in its own task whose pull request is merged before the tasks that implement it start; they depend on that task.

Requirements belong to the product owner. When you implement a change, keep tasks.md checkboxes of your own items current and fix specs your code made inaccurate, but do not change requirements (proposal.md, the specs/ deltas) on your own: ask first (factory_exit_ask) or leave it to the reviewer to hand to the product owner. A product owner (or the manager that owns the change) may merge its own spec pull request without review.

The last task of a change archives it: when your task says it is the final one of the change, run the official archive command (openspec archive NAME) after the work is done and commit the result in the same pull request. Factory tells the manager when every task of a change is done but the change is still active (unarchived, or with unchecked tasks.md items).

Keep product requirements in these files; keep task status, dependencies, execution and review in Factory. A Markdown checkbox never accepts a Factory task. Do not paste entire specifications into reports or decision values.
