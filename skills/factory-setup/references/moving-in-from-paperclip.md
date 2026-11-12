# Moving in from Paperclip

Goal: the Paperclip company's projects, open issues, agent prompts, skills and secrets live in Factory, and nothing
runs before the owner says so.

There is no `factory import paperclip` command yet: it waits for a documented Paperclip export or API format
(setup-assistant 2.2). Until then move in by hand, in this order, with Factory's own commands:

1. Projects, paused. For each Paperclip project: `factory projects create` (ID, name, repository URL), then at once
   `factory_pause scope=project target=ID reason="moving in"` so no agent starts while the rest arrives. No audit or
   review task is created for a project created this way.
2. Agents and prompts into the catalog. Pull the catalog (`factory catalog pull DIR`), write one agent document and
   one prompt document per Paperclip agent (`docs/catalog.md` has the format; keys Factory does not know are refused,
   e.g. a node's `company` or `token_file` belong in no catalog document). Check every document with
   `factory_catalog_validate documents={...}` and fix each error with the fix it names, then `factory catalog push DIR`.
3. Skills: install the library skills the agents used (`factory_skill_install`), or add the Paperclip skills as a
   skill source (web: Settings → Skills) and install them from there.
4. Open issues as backlog tasks: `factory tasks create` per open issue, in the backlog (not ready), assigned to the
   agent that owned it. Closed issues stay in Paperclip.
5. Secrets: list the names the agents need (`GET /secrets/needed` after step 2) and set each from a file with
   `factory_secret_set`. Never copy secret values through the chat.
6. When everything is in: `factory_resume scope=project target=ID` for each project (the preview lists what starts).

## Proof

- `factory_catalog_validate` with no documents says the stored catalog is valid, and `factory agents list` shows every
  moved agent.
- `GET /secrets/needed` lists every secret as set.
- After resuming, one moved backlog task made ready starts a run (`factory_runs`).
