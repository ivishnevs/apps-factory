## 1. Agent editor

- [ ] 1.1 Secrets section on the agent: own and inherited uses, add from the company's secrets (kind, variable name,
      file flag from the kind), remove; saves `spec.secrets` as a catalog change with a note; owner/admin only.

## 2. Secrets page

- [ ] 2.1 Uses per secret and "Give to an agent"; on add, optional agents; warning with agent names before deleting a
      used secret.

## 3. Checks

- [ ] 3.1 Missing or wrong-kind uses marked; a file secret as a string variable refused with a hint; tests (API and
      web).

## 4. Verify

- [ ] 4.1 On a sandbox company: add a file secret, give it to an agent from the agent editor, a run of that agent
      finds the file at the variable's path; remove the use and the next run does not get it.
