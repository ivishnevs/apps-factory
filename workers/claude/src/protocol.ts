// Mirrors internal/protocol: the provider-neutral Core ↔ Worker contract.
import { randomBytes } from "node:crypto";

export const PROVIDER = "claude";
export const COMMAND_SUBJECT = `factory.commands.agent.run.${PROVIDER}`;
export const DURABLE = `${PROVIDER}-worker-v1`;

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

export interface Envelope<T = unknown> {
  event_id: string;
  schema_version: number;
  type: string;
  correlation_id: string;
  causation_id?: string;
  occurred_at: string;
  payload: T;
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

export function newID(): string {
  return randomBytes(16).toString("hex");
}

export function wrap<T>(type: string, correlation: string, cause: string, payload: T): Envelope<T> {
  return {
    event_id: newID(),
    schema_version: 1,
    type,
    correlation_id: correlation,
    ...(cause ? { causation_id: cause } : {}),
    occurred_at: new Date().toISOString(),
    payload,
  };
}

// Invalid commands are terminated, matching the Go Worker's validation.
export function parseCommand(data: Uint8Array): Envelope<RunSpec> | undefined {
  try {
    const e = JSON.parse(new TextDecoder().decode(data)) as Envelope<RunSpec>;
    const spec = e.payload;
    if (e.schema_version !== 1 || e.type !== "agent.run.requested" || !spec?.id || spec.id !== e.correlation_id) {
      return undefined;
    }
    return e;
  } catch {
    return undefined;
  }
}
