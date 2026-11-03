## MODIFIED Requirements

### Requirement: Account and company tenancy
Core SHALL model accounts that own companies, and users that are members of one or more companies of their account with role `owner`, `admin` or `member`. A user's first sign-in SHALL create their account and one company they own. Agents, teams, projects, policies, quota keys, secrets, skills, nodes and all derived records SHALL belong to exactly one company.

#### Scenario: User in two companies
- **WHEN** a user who is a member of companies A and B calls the owner API for company A
- **THEN** Core SHALL return and modify only company A's data
