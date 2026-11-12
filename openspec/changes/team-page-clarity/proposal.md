Status: recorded 2026-11-05 by the owner. Mockup for task 1.1 made 2026-11-12 (https://claude.ai/artifact/KRePSTaBkscYfF2fK8HnsP); waits for the owner approval before implementation.

## Why

The team page shows three cards the owner could not understand: "Lead", "Onboarding" (an agent name) and "Shared
settings" (a profile name such as `gnome-forge-agent`). They look like mandatory roles to fill, but they are
three different things:

- **Lead** is the one agent of the team that reports to the owner: it gets the owner's messages that name no one,
  plans and hands work out. Required, exactly one.
- **Onboarding** is the audit assignee: the agent that gets the one-off audit task when an existing repository is
  added to the team with onboarding. Optional; it defaults to the lead.
- **Shared settings** is the team profile: defaults every agent of the team inherits (subscription and model,
  timeouts, concurrency, approvals, policies and review rules). Not a role at all.

Showing raw identifiers without explaining what they do, whether they are required and how to change them makes
the page look broken.

## What Changes (proposed)

- Explain each item in plain words where it is shown, with what happens because of it and whether it is required;
  hide what does not matter until it does (for example show "Onboarding: the lead, unless you choose another
  agent" instead of an agent name card).
- Show the team profile as the team's settings (subscription, model, limits, approvals, review rules) in readable
  form and let owners edit it there, instead of a profile name.
- Show who does what in the team in one place: the lead, who reviews what (code, requirements, art), who reports
  to whom.
- Let owners change the lead and the onboarding agent from the page with a short explanation of the effect.
- Use the same wording in the team wizard, docs and the agent cards.

## Impact

Web team page, team wizard and agent cards; possibly small API additions for changing the lead, the onboarding
agent and the profile fields (related: `structured-catalog`).
