## ADDED Requirements

### Requirement: Artifacts named in messages to people go with them
When a message to the owner, a forwarded question or a decision proposal names an artifact registered in its
project, Core SHALL add that version as a material. A URI, `<id>@<version>` or `<id> <version>` SHALL name a version;
a bare id with `-`, `_` or a digit SHALL name the latest. Unregistered names SHALL be ignored, the sender's own
materials SHALL NOT be repeated, and messages between agents SHALL be unchanged.

#### Scenario: Question names an artifact in plain text
- **WHEN** the lead asks the owner "may artifact stashy-lens-pouch-design go to print?" without materials
- **THEN** the question SHALL carry the latest registered version of stashy-lens-pouch-design

#### Scenario: Versions named in the text
- **WHEN** a review request to the owner says "compare pouch-design@v1 with factory://artifacts/pouch-design/v2" and attaches v1 itself
- **THEN** it SHALL carry v1 once (the sender's) and v2

#### Scenario: A plain word is not a link
- **WHEN** a message to the owner says "the logo stays" and an artifact with id logo exists
- **THEN** no material SHALL be added for it

#### Scenario: Forwarded to the owner
- **WHEN** an agent asks its manager about "pouch-design v1" and the manager forwards the question to the owner
- **THEN** the forwarded question SHALL carry pouch-design v1

### Requirement: Inbox links a message's artifacts to the Library
The inbox and the chat SHALL show a message's artifact versions with a link to each artifact's Library card. The web
Library SHALL accept an artifact id, list only that artifact's card and open it; the terminal inbox SHALL preview them
with `v`, one per press, without deciding anything.

#### Scenario: Owner opens the card
- **WHEN** the owner reads a question that carries stashy-lens-pouch-design v2 and follows its Library link
- **THEN** the Library SHALL show that artifact's card open in the viewer, with its versions

#### Scenario: Terminal preview
- **WHEN** the owner presses `v` on such a question in the terminal inbox
- **THEN** the artifact SHALL be previewed as an approval's artifact is, and `y`/`n` SHALL decide nothing
