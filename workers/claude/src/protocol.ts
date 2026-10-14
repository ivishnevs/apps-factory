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

export type TerminalStatus = "COMPLETED" | "FAILED" | "CANCELLED" | "UNKNOWN";

// How a run ended when the status alone does not say it (reliable-recovery D1); terminal events only.
// A failure outcome on a CANCELLED event makes Core end the run FAILED instead of cancelling the task.
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
