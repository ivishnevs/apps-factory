## 1. Dispatch

- [x] 1.1 `unwantedTx` shared by the claim re-check and `Dispatch`; Dispatch refuses a run the claim would end
      stale; test: a task parked on an escalation gets no run from repeated dispatches.
- [x] 1.2 Storm guard: `StaleRunCap` stale runs within `StaleRunWindow` refuse dispatch until the window passes,
      one `task.dispatch_throttled` feed event per window; test with a fake clock.
