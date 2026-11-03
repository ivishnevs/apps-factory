## MODIFIED Requirements

### Requirement: Seed from the repository
A company without a stored catalog SHALL get its first version from its catalog directory in the repository (`configs/` for the default company, `configs/<company>/` otherwise) and the prompts it names, with author `seed`. A company created by a user SHALL start with an empty catalog and get teams from presets or from scratch; templates in the repository SHALL NOT be used.

#### Scenario: First start on the new schema
- **WHEN** Core starts for the default company and no catalog version is stored
- **THEN** version 1 SHALL hold the documents of `configs/` and the prompts its agents name, and Core SHALL serve the same agents as the file catalog did

#### Scenario: Company created at sign-up
- **WHEN** a new user's company is created
- **THEN** its catalog SHALL have no teams until the user creates one
