## ADDED Requirements

### Requirement: Invitations to a company
An owner SHALL be able to invite a person to the company with a role through a single-use link that expires after
7 days; the person who opens it and signs in SHALL become a member with that role.

#### Scenario: Accepting an invitation
- **WHEN** an owner creates an invitation for the member role and another person opens it and signs in
- **THEN** that person is a member of the company and the link no longer works
