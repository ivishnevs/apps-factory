# company-catalog Specification

## Purpose
Store each company catalog in Core with versions, apply it without a restart and let clients edit it.

## Requirements

### Requirement: Catalog stored in Core with versions
A company's catalog (agents, profiles, policies, prompts, skills registry, company settings, quota keys, external MCP servers, node hooks) SHALL be stored in Core as versioned documents. Every version SHALL record its author and time, exactly one version per company SHALL be current, and a version SHALL be saved only when the catalog loader accepts it.

#### Scenario: Invalid change refused
- **WHEN** a client saves an agent that names an unknown profile
- **THEN** Core SHALL refuse the save with the loader's error naming the document, and the current version SHALL stay unchanged

#### Scenario: Rollback
- **WHEN** the owner rolls the catalog back to version 3 while version 5 is current
- **THEN** Core SHALL save a version 6 with the documents of version 3 and make it current

### Requirement: Apply without restart
A saved version SHALL become the company's catalog for new dispatches without restarting Core. A run SHALL keep the configuration snapshot captured at its dispatch.

#### Scenario: Prompt edited while a run is live
- **WHEN** the manager's prompt is changed while a manager run is running
- **THEN** that run SHALL keep its captured instructions and the manager's next run SHALL get the new prompt

### Requirement: Seed from the repository
A company without a stored catalog SHALL get its first version from its catalog directory in the repository (`configs/` for the default company, `configs/<company>/` otherwise) and the prompts it names, with author `seed`.

#### Scenario: First start on the new schema
- **WHEN** Core starts for the default company and no catalog version is stored
- **THEN** version 1 SHALL hold the documents of `configs/` and the prompts its agents name, and Core SHALL serve the same agents as the file catalog did

### Requirement: One Core serves many companies
Core SHALL resolve the catalog per request from the request's company and SHALL run its scheduling and background checks for every company with that company's catalog. Agents of two companies with the same name SHALL be independent.

#### Scenario: Two companies side by side
- **WHEN** companies `a` and `b` both define an agent `manager` with different prompts
- **THEN** a run of `a`'s manager SHALL get `a`'s prompt and a run of `b`'s manager SHALL get `b`'s prompt from the same Core

### Requirement: Clients edit their whole catalog
A company's owner SHALL be able to list, read, create, change and delete every document of its catalog, node hooks, external MCP servers, quota keys and command guards included. Skill contents, prices and built-in policies SHALL stay operator-only.

#### Scenario: Client adds a node hook
- **WHEN** a client saves a node hooks document for its company
- **THEN** that company's nodes SHALL receive it, and no other company's nodes SHALL
