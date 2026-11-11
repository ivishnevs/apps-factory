## ADDED Requirements

### Requirement: Choices are sent as options
An agent's question to a person that offers choices SHALL carry them as options, each shown as a button with free text still allowed. A question whose body lists choices without options SHALL get a hint in the tool's result.

#### Scenario: Profiles listed in the body
- **WHEN** an agent asks "which profile: ASC Key, Cheflet, Stashy" without options
- **THEN** the tool's result SHALL hint to resend the choices as options
