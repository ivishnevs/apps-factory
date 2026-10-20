// Mirrors internal/protocol: the RunSpec and run events the shim's adapter works with.

export interface RunSpec {
  id: string;
  project: string;
  agent: string;
  provider: string;
  workspace: string;
  prompt: string;
  instructions: string;
  timeout_seconds: number;
  session_id?: string;
  runtime_options?: Record<string, unknown>;
  openspec_change?: string;
  message_id?: string;
  task_id?: string;
  [field: string]: unknown;
}

// The shim's terminal evidence for the Go driver (driver.Result.Status); UNKNOWN is no evidence. The executor
// turns it into the run's outcome (succeeded, failed, the stop's outcome, lost), runtime-protocol.md.
export type TerminalStatus = "COMPLETED" | "FAILED" | "CANCELLED" | "UNKNOWN";

// What the shim knows about why a turn ended beyond its status (reliable-recovery D1); terminal evidence only.
export type Outcome = "failed" | "timed_out" | "idle_timeout" | "max_turns" | "setup_failed" | "paused_out";

export interface RunEvent {
  run_id: string;
  lease: string;
  session_id?: string;
  status?: TerminalStatus;
  result?: string;
  outcome?: Outcome;
  method?: string;
  data?: unknown;
}

export type EventKind = "started" | "progress" | "completed" | "failed";
