## ADDED Requirements

### Requirement: Plans with limits per account
Each account SHALL have a plan whose limits Core enforces; work over a limit SHALL wait with a reason instead of
failing, and nothing SHALL be deleted when a plan is lowered.

#### Scenario: Agent slots over the plan
- **WHEN** a company of an account on a plan with 2 agent slots has 2 runs going and a third task is ready
- **THEN** the third task waits with a reason naming the plan's limit and starts when a slot frees

### Requirement: Payments through a provider
Payments SHALL go through the provider's hosted checkout and portal; Core SHALL change an account's plan only from
the provider's signed webhooks and SHALL never handle card data.

#### Scenario: Upgrade
- **WHEN** the account's owner pays for Pro in the provider's checkout
- **THEN** the provider's webhook moves the account to Pro and its limits apply at once
