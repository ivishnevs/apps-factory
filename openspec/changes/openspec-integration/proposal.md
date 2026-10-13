## Why
Factory tasks carry an unchecked OpenSpec string and agents cannot read project specifications through the scoped gateway. Docker Core also lacks access to native Worker workspaces.

## What Changes
Add a filesystem integration for existing project workspaces: non-destructive structure initialization, bounded listing/reading, change reference checks, scoped API/CLI/MCP, explicit host-to-container path mapping, and validated task/run references. Documents stay in files, not SQLite. Authoring, workflow/schema validation and archive remain with the installed official OpenSpec tools.

## Capabilities
### New Capabilities
- `openspec-integration`: File-backed project specification access and checked task links.

### Modified Capabilities
None.

## Impact
Core workspace access, task dispatch/claim, native Worker preflight, Compose configuration and documentation. No new database migration or runtime loop.
