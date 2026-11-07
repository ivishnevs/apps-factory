Status: proposed 2026-11-07 by the owner ("I added the secret, but I cannot give it to the agent in the agent's UI").
Nothing implemented.

## Why

A secret reaches an agent's runs only through the agent's `spec.secrets` (core-secret-store D3), but no screen sets
it. On 2026-11-07 the owner added the file secret `GAME_SERVER_SSH_KEY` on the Secrets page for network-dev to
deploy the playtest server and found no way to give it to network-dev: the Secrets page shows only "Used by" and
"Agents are waiting for these secrets", the agent card has no secrets section, and since the company switched to
structured entities (structured-catalog) the owner does not edit agent YAML. It was done by hand through
`PUT /catalog/agents/network-dev` (catalog v51). The same gap hit `producer_compact_window` earlier
(agent-context-window interim).

## What Changes

- **Agent editor: Secrets section.** Owners and admins see the agent's secret uses (name, variable, string or file)
  and can add one from the company's secrets (picker, kind shown), set the variable name (default: the secret's name)
  and remove a use. Saving writes the agent's `spec.secrets` as a catalog change with a note, like any other agent
  edit; profile-inherited uses are shown read-only with their profile.
- **Secrets page: "Give to agents".** Each secret row shows its uses and a "Give to an agent" action (same picker
  from the other side); adding a secret can name the agents right away.
- **Checks and hints.** A use whose secret is missing or has the wrong kind is marked, as `/secrets/needed` does;
  removing a secret that agents use warns with their names. A file secret given as a string variable is refused.
- **Visible to the owner, not to agents.** Values are never shown; agents cannot grant themselves secrets (catalog
  writes stay owner/admin, or an approved agent action under agent-company-authority).

Not in scope: per-project or per-task secret scoping, secret rotation reminders.

## Impact

Web: agent editor (team builder / member card), Secrets page. API: existing `PUT /catalog/agents/{name}` and
`GET /secrets`; a small `GET /catalog/agents/{name}/secrets` view with inherited uses may be added. Spec:
`core-secret-store`. Related: `structured-catalog`, `agent-context-window` (its interim secret use).
