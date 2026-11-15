## ADDED Requirements

### Requirement: Pending questions keep their recipient awake
A question addressed to an agent that is still pending (not answered, forwarded, withdrawn or expired) SHALL keep a trigger on the recipient's task or conversation. A run that ends without acting on the question SHALL NOT mark it handled, and the recipient SHALL run again under the usual retry backoff and question-chain limits, with the question and the last refusal of an attempt to act on it in its brief.

#### Scenario: Forward refused, run ended
- **WHEN** an agent's forward of a question is refused for a missing brief and its run ends anyway
- **THEN** the question SHALL stay pending with that agent, the agent SHALL run again, and its brief SHALL name the question and the `brief_required` refusal

#### Scenario: Question forwarded
- **WHEN** the agent forwards the question successfully and its run ends
- **THEN** the question SHALL leave the agent's triggers and no extra run SHALL be scheduled for it
