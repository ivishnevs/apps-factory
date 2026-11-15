## ADDED Requirements

### Requirement: Run data retention
Core SHALL delete the events and log chunks of runs that ended more than the configured number of days ago
(`FACTORY_RUN_RETENTION_DAYS`, default 14, 0 keeps them forever), delete log contents nothing else uses, and trim the
run's stored spec to its small fields, keeping the run row, attempts, exits, costs and tool calls.

#### Scenario: Old run pruned
- **WHEN** a run ended 20 days ago and retention is 14 days
- **THEN** its events and log chunks are deleted, its log content is deleted unless another reference uses it, and its
  spec keeps only small fields

#### Scenario: Recent and live runs kept
- **WHEN** a run ended 2 days ago or is still running
- **THEN** nothing of it is pruned

### Requirement: Storage stays usable when full
The blob store SHALL NOT write content it already holds when that content is at most 1 MiB, and Core SHALL warn
from 80 % blob volume use.

#### Scenario: Restart on a full volume
- **WHEN** the blob volume has no free space and Core re-indexes skills it already stored
- **THEN** the puts succeed without writing

#### Scenario: Nearly full volume
- **WHEN** the blob volume is 80 % or more used
- **THEN** Core logs an error and adds one attention feed item per company per day naming what frees space
