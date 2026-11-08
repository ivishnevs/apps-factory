## ADDED Requirements

### Requirement: Visual attachments on messages and decisions
Core SHALL let an agent attach files of its worktree and references to existing materials to a message or a decision
proposal, and SHALL keep them on forward. The inbox, the chat and the decision card SHALL show attached images inline
with their captions, several images side by side, without the owner leaving Factory. A message to the owner that names
image or video files without attachments SHALL be accepted with a `visual_without_attachment` warning.

#### Scenario: Visual verdict in the inbox
- **WHEN** the producer asks the owner for a verdict and attaches three screenshots captioned classic, v1 and v2
- **THEN** the inbox card SHALL show the three images side by side with their captions next to the answer controls

#### Scenario: Path without attachment
- **WHEN** an agent asks the owner about "docs/qa/towers/side_by_side_v2.jpg" without attachments
- **THEN** the question SHALL be sent and the tool answer SHALL warn `visual_without_attachment`
