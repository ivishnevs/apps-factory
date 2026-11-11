## ADDED Requirements

### Requirement: Lead brainstorms with people before creating work
When an owner or admin writes to a team lead with an idea or goal rather than an explicit, fully described task,
the lead SHALL clarify, propose and agree a plan before creating tasks, SHALL decide on its own what fits the
company's direction, and SHALL post in the chat how it understood the request, the decisions it took and the tasks
it starts.

#### Scenario: Idea from the owner
- **WHEN** the owner writes "let's add a ranked mode" to the lead
- **THEN** the lead SHALL answer with questions, options and a proposed plan, and SHALL create tasks only after
  the plan is clear, posting a summary of its understanding, its own decisions and the tasks it created

#### Scenario: Explicit task
- **WHEN** the owner writes "create a task: fix the flaky integration test in phase 2, …" with a full description
- **THEN** the lead SHALL create that task without a discussion
