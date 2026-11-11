## ADDED Requirements

### Requirement: Skills page tells availability from use
The web Skills page SHALL describe an installed skill's scope as who may be given it ("Any agent may use", "Team NAME
only", "Agent NAME only") and SHALL list, for each installed skill, the agents whose `spec.skills.include` names it, or
say that no agent has it yet.

#### Scenario: Company-wide skill used by two agents
- **WHEN** `release-readiness` is installed company-wide and only the CEO, PM and App Store Publisher include it
- **THEN** its row SHALL read "Any agent may use" and "Used by appstore-publisher, ceo, product-manager", never "Every agent"
