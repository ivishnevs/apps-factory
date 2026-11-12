## ADDED Requirements

### Requirement: Plans with limits per account
Each account SHALL have a plan whose limits Core enforces while billing is on: Free allows 1 own node and no
invitations, Pro 5 own nodes and invitations. Own nodes SHALL be counted over every company of the account. Work
over a limit SHALL wait with a reason instead of failing, and nothing SHALL be deleted when a plan is lowered. An
account without a plan (created before billing, or on a Core with billing off) SHALL have no limits.

#### Scenario: Nodes over the plan
- **WHEN** a company on Free has one node and its owner connects a second machine
- **THEN** the connection is refused with a message that Pro allows 5 nodes and a link to Billing

#### Scenario: Inviting on Free
- **WHEN** an owner on Free tries to create an invitation link
- **THEN** it is refused with a message that inviting people starts with Pro

#### Scenario: Downgrade keeps everything
- **WHEN** an account with three own nodes moves from Pro to Free
- **THEN** all three stay connected, only the one the owner picked (else the oldest) takes work, and a queued run
  says the others are over the plan

#### Scenario: Trial
- **WHEN** a person signs up on a Core with billing on
- **THEN** their account is on Pro for 14 days without a card, then on Free unless it pays

#### Scenario: Billing off
- **WHEN** Core runs without FACTORY_BILLING
- **THEN** no plan limit applies and nobody gets a trial

### Requirement: Payments through a provider
Payments SHALL go through the provider's hosted checkout and portal; Core SHALL change an account's plan only from
the provider's signed webhooks (or the operator) and SHALL never handle card data. A webhook SHALL be applied once
and never over a newer one.

#### Scenario: Upgrade
- **WHEN** the account's owner pays for Pro in the provider's checkout
- **THEN** the provider's webhook moves the account to Pro and its limits apply at once

#### Scenario: Forged or repeated webhook
- **WHEN** a webhook arrives without a valid signature, or an already applied event arrives again
- **THEN** the forged one is refused and the repeated one changes nothing
