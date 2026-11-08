## ADDED Requirements

### Requirement: Context window per run kind
An agent's `runtime.context_window` SHALL accept one value for all runs or separate `work` and `conversation`
values. The node SHALL apply the value of the run's kind as the runtime's compaction window.

#### Scenario: Small conversation window, large work window
- **WHEN** the producer has `context_window: {work: max, conversation: 200k}`
- **THEN** its conversation runs SHALL compact at 200k tokens and its spec runs SHALL keep the model's maximum
