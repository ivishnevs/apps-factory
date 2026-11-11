## MODIFIED Requirements

### Requirement: Janitor and disk thresholds
The node SHALL remove worktrees of closed tasks after sealing, SHALL run the company's cleanup hooks, SHALL prune caches by age, and SHALL emit one low-disk alert and clean aggressively below the low threshold. Below the critical threshold the node SHALL stop claiming work and review runs but SHALL keep claiming conversation runs while free space stays above `disk.conversation_floor_gib` (default 1 GiB); an owner pause SHALL still stop every claim.

#### Scenario: Disk falls below the low threshold
- **WHEN** free disk space drops below the low watermark
- **THEN** the node SHALL prune caches of closed tasks immediately and Core SHALL emit exactly one `node.low_disk` attention event until space recovers

#### Scenario: Owner writes during a disk pause
- **WHEN** the node has 3.6 GiB free with a critical watermark of 5 GiB and the owner writes to the CEO
- **THEN** the node SHALL claim the CEO's conversation run and SHALL NOT claim any work or review run

#### Scenario: Disk nearly full
- **WHEN** free space falls below `disk.conversation_floor_gib`
- **THEN** the node SHALL claim no run of any kind until space recovers
