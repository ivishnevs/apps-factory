## D1 What a pruned run keeps
The run row and everything that is history or money (attempts, exits, costs, tool calls, task links) stays. Events,
log chunks and spec fields over 512 bytes go: they serve live viewing and debugging, which ends weeks after the run.
Readers of a run's spec read live runs (queued, claimed, current); an ended run's id, task, agent and project fields
are small and kept.

## D2 Contents are deleted only when unused
Log contents use the same check as artifact retention (`blobInUse`): another run's chunk, an artifact version, a
report attachment, a skill or a message attachment keeps the content.

## D3 Batches
200 runs per transaction, repeated until none is due, so the write-ahead log of one transaction stays small on a
volume that is nearly full.

## D4 Alert
LocalFS measures its volume with statfs. From 80 % used: slog error every hour, one attention feed item per company
per day (the Core keeps the day in memory, so a restart may repeat it once).
