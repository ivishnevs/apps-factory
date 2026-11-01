## Context

See proposal. Related: `openspec-in-git` (spec review rules start as prompts; enforcement belongs here),
`owner-web` (its out of scope: sign-in, multiple companies and users).

## Decisions (proposed; confirm in task 1)

### D1. Catalog stored in Core
The company catalog moves from files into Core storage with versions; the loader and validator stay the same
code over a different source. Prompts are stored as documents of the catalog. Seed: the repository's `configs/`.

### D2. Apply without restart
A saved catalog version becomes current atomically after validation; new runs resolve against it, running runs keep
their captured snapshot.

### D3. Review rule type
`type: review` with `roles`/`agents`, `required: true|false`, `reviewer` (agent or role), later `paths`. A required
review makes `factory_exit_done` refuse without an approval bound to the PR (the mechanism `done` already uses
when the project requires review). Without any rule: today's behaviour (prompt decides).

## Risks / Trade-offs

- Clients' node hooks, external MCP servers and command guards execute on nodes: a client must not be able to
  run arbitrary commands on shared nodes. Hosted nodes need isolation or these sections stay owner-only.
- Secrets referenced by a client's config (MCP servers, hooks) need a per-company secret store
  (`core-secret-store`).

## Open questions

- One Core process per company (today) or one Core serving many companies.
- Which catalog sections clients may edit themselves (node hooks, MCP servers, quota keys) versus only the
  operator.
- Whether prompts stay plain Markdown or get a structured editor (role, responsibilities, review rules).
