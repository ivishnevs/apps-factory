## ADDED Requirements

### Requirement: Plans with limits per account
Each account SHALL have a plan whose limits Core enforces; work over a limit SHALL wait with a reason instead of
failing, and nothing SHALL be deleted when a plan is lowered.

#### Scenario: Connectors over the plan
- **WHEN** a company on Free has one connector and its owner connects a second machine
- **THEN** the connection is refused with a message that Pro allows 5 connectors and a link to Billing

#### Scenario: Inviting on Free
- **WHEN** an owner on Free tries to create an invitation link
- **THEN** it is refused with a message that inviting people starts with Pro

### Requirement: Payments through a provider
Payments SHALL go through the provider's hosted checkout and portal; Core SHALL change an account's plan only from
the provider's signed webhooks and SHALL never handle card data.

#### Scenario: Upgrade
- **WHEN** the account's owner pays for Pro in the provider's checkout
- **THEN** the provider's webhook moves the account to Pro and its limits apply at once
