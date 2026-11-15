## 1. Retention
- [x] 1.1 `PruneRunData`: events, log chunks, unused log contents, spec trim, batches, feed item; test with fixtures
      (old, recent and live runs; content shared with another run; 0 days; second pass).
- [x] 1.2 `FACTORY_RUN_RETENTION_DAYS` (default 14) and the daily pass next to artifact retention.

## 2. Full volume
- [x] 2.1 LocalFS: content up to 1 MiB already stored is not written again; test on a directory that cannot be written.
- [x] 2.2 Hourly blob volume check, operator log and a daily attention feed item from 80 %; test.

## 3. Docs
- [x] 3.1 Operator docs: the setting, the alert, what frees space.
