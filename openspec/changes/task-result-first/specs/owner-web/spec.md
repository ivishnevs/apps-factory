## ADDED Requirements

### Requirement: A task's result leads its materials
A finished task's page SHALL lead with the materials the agent named as the result in its last accepted submission or done exit, and without such materials with the newest material that has content. Materials outside the result SHALL be folded under "Earlier versions", grouped by artifact with their approvals, and every row SHALL name its artifact and version.

#### Scenario: Screenshots task with samples
- **WHEN** the owner opens a done screenshots task whose last submission named the final gallery, and that produced two approved samples and a storyboard
- **THEN** the final gallery SHALL be the lead card and the samples and the storyboard SHALL be under "Earlier versions" with the board's approval comments
