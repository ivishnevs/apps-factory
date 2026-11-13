Status: approved by the owner 2026-11-13. Implemented 2026-11-13: Core had no model list, so `config.RuntimeModels` (per runtime, led by
"default"; a pick-list only, any model name stays valid) and `config.RuntimeEfforts` (Efforts when the driver reads
the effort option) are served by `GET /catalog/runtimes`; the team member payload gained `effort` (resolved with the
profile); the run payload already carried `spec.runtime_options` (model, effort), so the run page reads it unchanged.
The agent edit form has Effort (Default = the profile's or the runtime's) and the Model pick-list with "Custom…"; the
add-agent form got the Model pick-list only (preset overrides have no effort field: set it in the edit form after
adding). A refused effort maps to the form's Effort field.

## Why

Agents carry a reasoning effort in the catalog (`spec.runtime.effort`: low, medium, high, xhigh, max; Claude and
Codex) and every run receives it, but the web shows it nowhere: the agent form edits Runtime and Model only, the team
member card shows the runtime and model, and the run page shows the model. The owner could not see that the CEO and
the product manager ran on `low` (2026-11-13) and had to ask for a catalog pull to change it. The model is also a free
text field, so a typo breaks runs.

## What Changes

- **Agent form:** an Effort field (low, medium, high, xhigh, max; empty keeps the runtime's default), saved to the
  catalog with history like the other agent fields. The Model field offers the runtime's known models to pick from and
  still accepts a custom value.
- **Team member card:** shows model and effort ("runs on Claude (claude-opus-5-5 · high)").
- **Run page:** shows the effort the run actually used (from the run's config snapshot / runtime options) next to the
  model.
- Works for every runtime that accepts effort; a runtime without it shows none.

## Impact

- Specs: `owner-web` (agent form, member card, run page).
- Code: web agent form (`web/src/components/app/team/builder.tsx`), member card (`web/src/components/app/team/catalog.tsx`),
  run page (`web/src/app/c/[company]/runs/[id]/page.tsx`); Core only if the agent or run payloads lack the effort or a
  runtime's model list.
