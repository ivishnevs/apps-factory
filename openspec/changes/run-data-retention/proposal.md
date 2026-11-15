Status: accepted 2026-11-15 (owner: "делай срочно все") after a production company's Factory ran out of storage.

## Why

On 2026-11-14 a production Core's blob volume (5 GB) and its Postgres volume filled up. Uploads failed with "no space
left on device", owner approvals with images stopped, and after a restart Core crash-looped: it re-indexes its
built-in skills at every start and the blob store wrote each one to a temporary file before noticing it already had
it. Nothing ever removes run data: every run keeps its event stream (`events`, 1 GB), its log chunks (1.6 GB of
blobs) and its full spec with the prompt (~46 KB a run; 2.2 GB, mostly from a run storm on one day). Artifact
retention exists but is off unless a project sets it, and nobody is told before the volume is full.

## What Changes

- **Run data retention.** `FACTORY_RUN_RETENTION_DAYS` (default 14, 0 keeps forever): once a day Core deletes the
  events and log chunks of runs that ended longer ago, deletes log contents nothing else uses, and trims the run's
  stored spec to its small fields. The run row, attempts, exits, costs and tool calls stay. Work goes in batches of
  200 runs so a backlog never needs one huge transaction on a nearly full database.
- **Repeated small puts write nothing.** The blob store hashes content up to 1 MiB in memory first and does not
  write it when it already has it, so Core starts and re-indexes its skills on a full volume (and retention can then
  free space).
- **Storage alert.** Every hour Core measures the blob volume; from 80 % it logs an error for the operator and adds
  one attention item a day to every company's feed, naming the settings that free space.

Out of scope: measuring the database volume (Core does not know its size; the operator's provider shows it), the
run storm itself (separate change), quotas per company (`BlobQuota` exists).
