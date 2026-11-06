## ADDED Requirements

### Requirement: Node-owned runtime logins
A node SHALL run Claude Code and Codex with configuration and credential directories of its own, separate from the
machine user's, logged in through `factory-node login`, so that the person logging in or out in their own terminal
does not change the account the agents use.

#### Scenario: Person switches their terminal to a work account
- **WHEN** the person logs their own Claude Code in to a work subscription
- **THEN** the node's runs SHALL keep using the account the node was logged in with

### Requirement: Account change stops the runtime
A node SHALL record which account each runtime uses (plan, organisation, a hash of the email) and check it before
each run. A different account or a logout SHALL pause that runtime on the node, keep its runs queued, and tell the
owner which account it is now, with a choice to continue on it or keep it paused.

#### Scenario: Account changed
- **WHEN** the Claude account of a node changes from a personal plan to an organisation's plan
- **THEN** no Claude run SHALL start on that node until the owner continues, and the owner SHALL get an Inbox item
  naming the new plan and organisation

#### Scenario: Limits per account
- **WHEN** one account hits its rate limit
- **THEN** the limit SHALL be recorded for that account only
