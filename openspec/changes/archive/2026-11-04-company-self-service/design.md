## Context

See proposal. Related: `openspec-in-git` (spec review rules start as prompts; enforcement belongs here),
`owner-web` (its out of scope: sign-in, multiple companies and users), `postgres-tenancy` (every table, token and
NATS subject is already company-scoped; only Core's in-memory catalog serves one company, `FACTORY_COMPANY`).

Today `config.LoadCompanyCatalog(root, company)` reads `configs/` (default company) or `configs/<company>/`:
`agents/`, `profiles/`, `policies/`, `node-hooks/`, `skills.yaml`, `company.yaml`, `quota_keys.yaml`,
`mcp_servers.yaml`, plus prompt files named by `spec.prompt.file` relative to the factory root (`prompts/*.md`) and
skill contents under `skills/`. Core keeps the result in `Core.Agents`, `Skills`, `NodeHooks`, `DefaultReport`,
`AuditAssignee` (about 45 uses) and serves `c.company()` (22 uses, scheduler and loops included).

## Decisions (owner 2026-11-01)

### D1. Catalog stored in Core, as versioned documents
A company's catalog is a set of documents stored in Core: path (as in the file layout, relative to the factory
root: `configs/<company-dir>/agents/manager.yaml`, `prompts/manager.md`, ...) and UTF-8 content (at most 256 KiB
each). A version is an immutable full snapshot (`catalog_versions`: company, version, author, created_at, note;
`catalog_documents`: company, version, path, content); exactly one version per company is current. The loader
stays the same code over an `fs.FS` instead of the disk: a company's view is its stored documents, and outside its
catalog directory the factory root on disk underneath (shared prompts as templates, `skills/` contents,
`configs/prices.yaml`). Inside the company's catalog directory only stored documents count. Validation is exactly
`factory config validate` (the loader), so a version that does not load is never saved.

Seed: a company with no stored catalog gets its first version from its directory in the repository
(`configs/` for `default`, `configs/<company>/`) and the prompts it names, author `seed`.

### D2. Apply without restart
Saving a version validates it, stores it and makes it current in one transaction; Core swaps the company's loaded
catalog after the commit. New dispatches resolve against it; running runs keep their captured config snapshot.
Rollback is saving a copy of an older version as a new one (history stays linear). One Core process owns the
cache (the deployment is one Core); a second process would see the change on its next load.

### D3. Review rule type
`type: review` with `roles`/`agents`, `required: true|false`, `reviewer` (agent or role). A required review makes
`factory_exit_done` refuse (`409 review_required`) a task of a matching agent with a repository unless an approval
is bound to its pull request (`pr_approvals`, the mechanism `review_after_conflicts` uses); `reviewer` is the
default reviewer of `factory_exit_submit`. Without any review rule: today's behaviour (the prompt decides). Path
rules (`paths`, e.g. `openspec/specs/**` needs the product owner) come later (task 5.3): they need the PR's changed
files in the exit facts.

### D4. One Core, many companies
Core serves every company: the catalog is resolved per request from the request's company (`companyFrom`: owner
token, node token, run credential), and Core's loops (scheduler, stalls, prices, Telegram digest) run per company
with that company's catalog. `FACTORY_COMPANY` remains the company of the owner token and the CLI default.
`Core.Agents` and friends become `c.catalog(company)`.

### D5. What clients edit
Everything in their catalog: agents, profiles, policies (command guards included), prompts, skills registry,
company settings, quota keys, external MCP servers and node hooks. Clients connect their own nodes, so hooks,
MCP servers and guards run on the client's machines. Operator-only: skill contents (`skills/` on the Core host),
`configs/prices.yaml`, the built-in policies. Revisit D5 if hosted shared nodes appear.

### D6. Prompts stay Markdown
Prompts are plain Markdown documents of the catalog; a structured editor is out of scope.

## Risks / Trade-offs

- Secrets referenced by a client's config (MCP servers, hooks) need a per-company secret store
  (`core-secret-store`); until then they are node-side secrets as today.
- Without sign-in (`owner-web` out of scope) every company is administered with the operator's owner token plus
  an explicit company; per-company owner tokens arrive with company provisioning (task 4.1).
- The loader over `fs.FS` touches every config read; the disk catalog keeps working through the same path
  (`os.DirFS`), so tests of today's catalog stay the guard.
