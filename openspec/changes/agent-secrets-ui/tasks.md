## 1. Agent editor

- [x] 1.1 Secrets section on the agent: own and inherited uses, add from the company's secrets (kind, variable name,
      file flag from the kind), remove; saves `spec.secrets` as a catalog change with a note; owner/admin only.
      Done: `GET /catalog/agents/{name}/secrets` (own uses, uses inherited from the profile with its name, MCP header
      secrets with the server; each `set`/`missing`/`wrong_kind`) and `PUT /catalog/agents/{name}/secrets`
      (`{base, secrets}`: the agent's own uses; note `agent X secrets: +NAME as ENV (file), -NAME`) in
      `internal/core/agent_secrets.go`; web `components/app/team/agent-secrets.tsx` in the agent editor
      (`AgentFormSheet`): each add or remove is saved at once and the editor's draft takes the new base and uses.
      Writes need owner/admin (`requiredRole`: writes under `/catalog`); agents reach only `/agent/*`.

## 2. Secrets page

- [x] 2.1 Uses per secret and "Give to an agent"; on add, optional agents; warning with agent names before deleting a
      used secret. Done: `POST /secrets/{name}/agents` (`{base, agents, env}`; one catalog change, note
      `secret NAME given to a, b as ENV`; file flag from the secret's kind; 404 for an unknown secret or agent); the
      page's "Give to an agent" sheet (agents of every team, those already using it left out, variable defaulting to
      the name), agent checkboxes when adding a secret, and the delete confirmation naming the agents (and MCP
      servers).

## 3. Checks

- [x] 3.1 Missing or wrong-kind uses marked; a file secret as a string variable refused with a hint; tests (API and
      web). Done: the editor marks `missing` and `wrong_kind` uses; Core refuses a new or changed use whose secret is
      set with the other kind (file as text, or text as file) with a hint in the `secrets` field, in the secrets
      route and in `PUT /catalog/agents/{name}`; a use the agent already had stays (marked) so its other uses can
      still change. Tests: `internal/core/agent_secrets_test.go` (`TestAgentSecretsAPI`). Web: `tsc`, lint and
      `next build` (the web app has no unit test runner).

## 4. Verify

- [x] 4.1 On a sandbox company: add a file secret, give it to an agent from the agent editor, a run of that agent
      finds the file at the variable's path; remove the use and the next run does not get it. Done as an automated
      end-to-end test instead of a live sandbox: `internal/node/runner/agent_secrets_e2e_test.go`
      (`TestAgentSecretsEndToEnd`): Core over HTTP with its stored catalog, a real enrolled node with a fake runtime;
      the file secret is added and given through the editor's route, the run's runtime reads the file at
      `$GAME_SERVER_SSH_KEY` (same bytes); after the use is removed the next run has neither the variable nor the
      file.
