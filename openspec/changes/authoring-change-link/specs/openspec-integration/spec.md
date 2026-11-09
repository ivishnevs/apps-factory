## ADDED Requirements

### Requirement: Authoring change links
A task SHALL link an OpenSpec change either as `implements` or as `authors`. The node SHALL check an `implements`
change before every run as today and SHALL NOT require an `authors` change before a run. Core SHALL accept the submit
or done exit of an `authors` task only when the change is on its branch. A lead's task linking a change that does not
exist yet SHALL default to `authors`.

#### Scenario: Producer writes a new change
- **WHEN** the producer's task "Spec: OpenSpec change X" links X as `authors` and X is not on the default branch
- **THEN** the run SHALL start, and the task SHALL finish only with `changes/X/proposal.md` on its branch

### Requirement: Change missing escalation does not loop
A `change_missing` escalation SHALL offer `unlink` (clear the link and continue) besides `retry` and `cancel`, SHALL
offer `retry` only when the default branch or the task's link changed since the failed check, and a repeated
`change_missing` on the same commit and link SHALL keep the open escalation instead of opening a new one.

#### Scenario: Owner answers retry with nothing changed
- **WHEN** the owner answers retry and the next run ends `change_missing` on the same commit and link
- **THEN** no new question SHALL open, and the open one SHALL say that retry fails until the change is merged or the
  link is removed
