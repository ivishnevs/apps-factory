Status: decided 2026-11-06 by the owner (see "Decision"); implemented 2026-11-07.

## Why

As an experiment the owner asked the game team's lead (`producer`) to remove another team (`product`), and it did:
catalog version 28, author `producer`, "Remove team product (builder, product-qa, profile, policy, prompts) at
owner request". The owner wants to know whether team leads should be able to manage the company and other teams at
all, how this authority is configured today, whether it is visible and editable in the UI, and what the defaults
are.

How it works today (checked 2026-11-05):

- Every agent whose tool policy does not deny it has `factory_catalog_get` and `factory_catalog_change`. The
  change tool edits the **whole company catalog** (all teams, agents, prompts, policies, skills, MCP servers, node
  hooks), not only the agent's own team.
- The only gate is an approval: `factory_catalog_change` always needs one, and only owners and admins may approve it
  (`adminApprovedCalls`, because a catalog change can hand a secret to an MCP server address). Members cannot.
- Role does not matter: a developer could propose deleting a team just as the lead did; tools mode `denylist` in
  the team profile allows everything not listed.
- The approval records the agent and its reason, but not which person approved (`people-attribution`), and the UI
  shows the change as a list of documents, not as "deletes team product and 2 agents".
- None of this is shown on the team page; tools, approvals and policies are YAML documents (`structured-catalog`).

## Options to weigh

- **Keep as is** (any agent, any change, people approve each one): flexible, safe only as long as people read the
  approvals carefully.
- **Scope by default**: an agent may change only its own team's agents, prompts and skills; company-wide changes
  (other teams, company settings, secrets, MCP servers, node hooks, quota keys) need an explicit grant to that
  agent (for example a "company manager" capability given only to chosen leads).
- **By role**: only the lead (manager role) of a team may propose catalog changes at all; other roles ask the lead.
- **Destructive changes always need the owner** (deleting a team, an agent, a project), even when admins may approve
  the rest.

## What Changes (proposed, after the decision)

- Explicit scopes for agents' catalog changes (own team / company), with safe defaults (own team only; leads only).
- The UI shows each agent's authority in plain words on the team page ("can change its team: yes, needs your
  approval; can change other teams: no") and lets owners grant or revoke it.
- Approvals of catalog changes are explained in plain words (what is added, changed and deleted, which teams are
  affected), destructive ones highlighted, and record who approved them.
- Docs: what agents may change in the company and how to restrict it.

## Impact

Agent tools and authority (factory_catalog_change scope checks in Core), approvals UI, team page, presets
(defaults), docs. Related: `structured-catalog`, `people-attribution`, `team-page-clarity`, `review-coverage`.

## Decision (2026-11-06)

- Only a team's lead (manager role) may propose catalog changes, and only for its own team (its agents, prompts,
  skills, team settings). Other roles ask their lead.
- Company-wide changes (other teams, company settings, secrets, MCP servers, node hooks, quota keys) need an explicit
  grant to that lead (a "company manager" capability given by an owner).
- Destructive changes (deleting a team, an agent or a project) always need the owner, even where admins may approve
  the rest.
- Every change still needs a person's approval, explained in plain words.

## Implementation (2026-11-07)

- Core: `factory_catalog_change` is checked when the agent asks for approval and when it runs: lead (team lead or
  role manager) only; own team's documents only (team, agents, prompts, the team's profile, policies only its agents
  use); company documents and other teams need `spec.authority.company`; a change granting that is refused (people
  only). Approving a deletion of a team or an agent needs the owner role.
- Web: the team page's Lead card says what the lead may change and has the owner's "Allow company changes" / "Limit to
  this team"; catalog change approvals show what changes and what is deleted, deletions highlighted.
- Decided during implementation: the grant is a field of the lead's agent document (`spec.authority.company`), so it
  lives with the agent and its history; "who approved" waits for `people-attribution`.
