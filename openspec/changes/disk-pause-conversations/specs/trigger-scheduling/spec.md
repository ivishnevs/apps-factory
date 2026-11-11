## ADDED Requirements

### Requirement: Node verdict names a disk pause
A task's queued verdict SHALL tell a node paused for critical disk from a node paused by the owner. A disk pause SHALL give reason `node_disk_critical` with the node, its free space and watermark, and the clearer to free disk on that node, which resumes by itself; an owner pause SHALL name the owner's reason and the clearer to resume the node. "Start or fix a node" SHALL be given only for nodes that are offline or cannot run the assignee.

#### Scenario: Work task during a disk pause
- **WHEN** the only capable node is paused at 3.6 GiB free with a critical watermark of 5 GiB
- **THEN** the task's verdict SHALL be `node_disk_critical` saying "free disk on tvorog-mac; it resumes by itself above 5 GiB", not "start or fix a node"

#### Scenario: Conversation brief during a disk pause
- **WHEN** a conversation run starts on a node paused for critical disk
- **THEN** its brief SHALL say that the disk is critical and that new work waits until space is freed
