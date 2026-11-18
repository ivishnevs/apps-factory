## ADDED Requirements

### Requirement: Codex compacts at the window through the driver
The Codex driver SHALL NOT pass a run's `context_window` to Codex as `model_auto_compact_token_limit` (with it set
Codex does not compact: openai/codex#16033). When the run has a window and its last turn's context
(`tokenUsage.last.totalTokens`) reached it, the driver SHALL call `thread/compact/start` after that turn and wait for
the compaction to finish, at most a bounded time. A failed, refused or timed-out compaction SHALL NOT change the run's
result. The compaction's token usage SHALL count towards the run. Without a window the driver SHALL NOT compact.
Core SHALL drop `context_window` from a Codex run claimed by a node that does not list the `codex_manual_compact`
feature.

#### Scenario: A conversation outgrows its window
- **WHEN** artist-2d's conversation has `context_window: 200k` and a run's last request used 250k tokens
- **THEN** the driver SHALL compact the thread after the turn, and the next run SHALL resume the compacted thread

#### Scenario: Default window
- **WHEN** a Codex agent's window for the run kind is `max` or unset
- **THEN** the driver SHALL not compact and Codex SHALL compact at its own limit

#### Scenario: An older node
- **WHEN** a node without the `codex_manual_compact` feature claims a Codex run with a window
- **THEN** the run SHALL reach it without `context_window`
