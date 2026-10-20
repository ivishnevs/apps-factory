Draft (idea, not scheduled): the requirement below states the intent for discussion only.

## ADDED Requirements

### Requirement: Escalate long parks without activity
Core SHALL open one self-clearing escalation to the assignee's manager when a task has been parked (children, ask, block or escalation; not a wait with a deadline, not an ask locked to the owner) longer than the company's configured threshold (default 24h) with no activity on the task, its children or the tasks it waits on.

#### Scenario: Children assigned but not moving
- **WHEN** a task has been parked on its children for 25 hours and no child ran or changed status
- **THEN** the task's manager SHALL get one escalation naming the park, which SHALL clear when a child moves or the park clears
