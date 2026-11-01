# Stored catalogs and companies

A company's catalog (agents, profiles, policies, prompts, skills registry, `company.yaml`, quota keys, external
MCP servers, node hooks) lives in Core as versioned documents (company-self-service). One Core serves every
company: a request's company comes from its token (the operator's `FACTORY_TOKEN` serves `FACTORY_COMPANY`, a
company owner token its company, a node token its node's company, a run credential its run's company), and
Core's loops (scheduling, expiry, stalls, prices) run per company with that company's catalog.

## Documents and versions

A document is a path relative to the factory root, as in the file layout, and its UTF-8 content (at most
256 KiB, 1000 per version): `configs/<dir>/{agents,profiles,policies,node-hooks}/NAME.yaml`,
`configs/<dir>/{skills,company,quota_keys,mcp_servers}.yaml` and `prompts/**.md`, where `<dir>` is the
company's catalog directory (`configs` for `default`, `configs/<company>` otherwise). Inside the company's
sections only its documents count; prompts it does not hold and skill contents come from the factory root on
the Core host. Skill contents, `configs/prices.yaml` and the built-in policies stay operator-only.

A version is an immutable full snapshot with author, time and note; the latest is current. A save names the
version it was made on (`base`): a newer current version refuses it (409), and the whole set must load with the
same loader as `factory config validate`, so a broken catalog is never saved (400 with the loader's error,
naming the document). The new version applies at once without a restart; a running run keeps the
configuration captured at its dispatch. A rollback saves an older version's documents as a new version.

Seed: when Core starts and its own company has no stored version, version 1 is made from the repository
(`configs/` or `configs/<company>/` and the prompts its agents name). After that, editing `configs/` on disk
changes nothing for that company: change the catalog through the API, the CLI or the web UI.

## Owner API, CLI and web

- `GET /catalog[?version=N]` (version and document list), `GET /catalog/document?path=P[&version=N]`,
  `GET /catalog/history`, `GET /catalog/team` (the team as resolved).
- `POST /catalog` `{base, put: {path: content}, delete: [path], note, dry_run}`: apply a change on top of
  `base` (`dry_run` validates without saving); `POST /catalog/rollback` `{base, to, note}`.

```sh
factory catalog                     # current version and documents
factory catalog pull ./my-catalog   # files + .catalog-version
$EDITOR ./my-catalog/prompts/manager.md
factory catalog push ./my-catalog "shorter manager prompt"
factory catalog get prompts/manager.md [VERSION] | put PATH FILE [NOTE] | rm PATH | validate PATH FILE
factory catalog history | rollback VERSION [NOTE]
```

The web UI's Team page shows the team, edits any document (a diff before saving, validation, the loader's
refusal at the document) and lists the history with rollback.

Agents read the catalog with `factory_catalog_get` and propose changes with `factory_catalog_change`
`{base, put, delete, note, request_id}`: it always needs the owner's approval of that exact call
(`factory_owner_request_approval`), and the saved version records the agent as author.

## Companies and owner tokens

The operator (`FACTORY_TOKEN`) creates a company from a starter template; the answer carries the company's first
owner token (`fot_…`), shown only once. An owner token reaches its company's owner routes only, never company
management; owners issue and revoke more tokens (`/owner-tokens`).

```sh
factory templates                                   # solo, product-team, full-team
factory companies create acme product-team "Acme Inc."
factory companies list
FACTORY_TOKEN=fot_… factory owner-tokens create laptop | list | revoke ID
```

Templates live in `configs/templates/<name>/` (catalog sections in the layout of a company directory, skill paths
relative to `configs/<company>/`) with role prompts in `prompts/roles/`: `solo` (one builder), `product-team`
(product owner, developer, reviewer) and `full-team` (product owner, designer, developer, QA). The team
templates require review of the developer's work and the product owner's approval of requirement changes
([policies](policies.md#review-rules)).
