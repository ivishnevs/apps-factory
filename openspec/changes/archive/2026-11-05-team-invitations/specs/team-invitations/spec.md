## ADDED Requirements

### Requirement: Invitations to a company
An owner SHALL be able to invite a person to the company with the admin or member role through a single-use link
that expires after 7 days; the person who opens it and signs in SHALL become a member with that role. Only the
hash of a link's token SHALL be stored. Admins and members SHALL NOT create, list or revoke links.

#### Scenario: Accepting an invitation
- **WHEN** an owner creates an invitation for the member role and another person opens it and signs in
- **THEN** that person is a member of the company and the link no longer works

#### Scenario: Signing up from a link
- **WHEN** a person without an account signs in from an open invitation link
- **THEN** they join the invitation's company and get no company of their own and no setup wizard

#### Scenario: Revoked or used link
- **WHEN** a link was revoked, used or has expired
- **THEN** its page says so, accepting it is refused, and signing up from it gives the person their own company

### Requirement: Memberships across accounts
A membership SHALL join a user to a company of any account; the company's account SHALL stay its owner.

#### Scenario: Two companies of two accounts
- **WHEN** a person with their own company accepts an invitation to another account's company
- **THEN** they work in both companies with their roles there
