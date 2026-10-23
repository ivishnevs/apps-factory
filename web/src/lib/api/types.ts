// JSON shapes of the owner API (`factory web`, internal/web), mirroring the Go structs' json tags
// (internal/tui, internal/storage, internal/tasks, internal/messaging, internal/approvals, internal/core).
// Times named *_at are Unix seconds unless typed string (RFC 3339).

export interface Clearer { who: string; action: string }
export interface Trigger { seq: number; kind: string; actor: string; authority: boolean; ref_id?: string }
export interface Verdict {
  task_id: string; status: string; reason: string; detail?: string; clearer: Clearer; pending_triggers?: Trigger[];
}

export interface Artifact { id: string; version: string; uri: string; size?: number; name?: string; sha256?: string }
export interface Submission { summary: string; commit?: string; checks?: string[]; artifacts?: Artifact[] }

export type TaskStatus = "BACKLOG" | "READY" | "IN_PROGRESS" | "IN_REVIEW" | "REWORK" | "BLOCKED" | "DONE" | "CANCELLED";

export interface Task {
  id: string; project_id: string; title: string; description: string; status: TaskStatus; priority: number;
  assignee: string; created_by: string; parent_task_id?: string; openspec_change?: string; dependencies: string[];
  created_at: number; updated_at: number; version: number; current_run_id?: string; attempt: number; result?: string;
  submission?: Submission; review_feedback?: string; block_reason?: string; cancel_requested: boolean;
  origin_task?: string; origin_run?: string; attempts_no_progress?: number; fail_streak?: number; not_before?: number;
  progress_at?: number; implementer?: string; reviewer?: string; blocked_on?: string[]; model?: string;
  required_artifacts?: Artifact[]; wait_message_id?: string;
  // Added by the overview: the why reason and who clears it.
  reason: string; clearer: Clearer;
}

export interface Agent { name: string; role?: string; reports_to?: string; provider?: string; model?: string }

export interface Project {
  id: string; name: string; description: string; workspace: string; version: number; created_at: number;
  updated_at: number; repo_url?: string; default_branch?: string; rules?: string;
}

export interface QueuedReason { reason: string; detail: string; until?: number; nodes?: unknown[] }
export interface RunStatus { id: string; agent: string; task_id?: string; status: string; started_at?: number; queued?: QueuedReason }
export interface ProjectStatus {
  project: Project; runs: RunStatus[]; tasks: Record<string, number>; waiting: Verdict[]; runnable: number; inbox: number;
}

export interface Pause { scope: string; target?: string; reason?: string; actor: string; created_at: number; until?: number }
export interface PendingCounts { inbox: number; approvals: number; alerts: number; inspect: number; in_review: number; total: number }

export interface Health { status: string; database?: string; company: string; explicit_exits?: string; default_report?: string }

export interface NodeUse { runs: number; resources: Record<string, number> }
export interface FactoryNode {
  node_id: string; name: string; labels?: string[]; slots?: number; drivers?: { name: string; ok: boolean; [k: string]: unknown }[];
  health: Record<string, unknown>; registered_at: number; last_seen: number; online: boolean; in_use: NodeUse;
  [k: string]: unknown;
}

export interface CostGroup {
  key: string; runs: number; input_tokens: number; output_tokens: number; cache_read_tokens: number;
  cache_write_tokens: number; cost_usd: number; sources: Record<string, number>;
}
export interface RunCost {
  run_id: string; task_id?: string; project_id?: string; agent: string; provider?: string; model?: string; outcome: string;
  input_tokens: number; output_tokens: number; cache_read_tokens: number; cache_write_tokens: number; cost_usd: number;
  cost_source: string; recorded_at: number;
}
export interface CostReport { runs?: RunCost[]; groups?: CostGroup[]; total: CostGroup }
export interface CostView { since: string; days: CostReport; agents: CostReport; models: CostReport; projects: CostReport }

export interface Usage { model: string; input: number; output: number; cache_read?: number; cache_write?: number; cost_usd?: number; cost_source?: string }
export interface RunSpec {
  id: string; project: string; agent: string; provider: string; task_id?: string; attempt?: number; openspec_change?: string;
  prompt: string; timeout_seconds: number; session_id?: string; runtime_options?: Record<string, unknown>; [k: string]: unknown;
}
export type RunPhase = "QUEUED" | "CLAIMED" | "RUNNING" | "CANCELLING" | "ENDED";
export interface Run {
  started_at: number; finished_at: number; spec: RunSpec; status: RunPhase; session_id: string; result: string;
  usage?: Usage[]; outcome?: string;
}

// A run event: the envelope's payload is the worker event (protocol.Event); data holds the runtime's raw
// stream line (Claude stream-json / Codex JSON-RPC), parsed like internal/tui/transcript_parse.go.
export interface RunEventPayload {
  run_id: string; session_id?: string; status?: string; result?: string; method?: string; data?: unknown;
  usage?: Usage[]; outcome?: string; exit_state?: string; quota?: { resets_at?: number; [k: string]: unknown };
}
export interface Envelope {
  event_id: string; schema_version: number; type: string; correlation_id: string; causation_id?: string;
  occurred_at: string; payload: RunEventPayload;
}
export interface RunEvent { seq: number; envelope: Envelope }

export interface ToolCall {
  id: string; run_id: string; agent: string; project_id: string; tool: string; request_id: string; status: number;
  created_at: number; approval_id?: string;
}

export interface FeedItem {
  sequence: number; event_id: string; type: string; entity_id: string; project_id: string; created_at: string;
  status?: string; summary?: string; task_id?: string; run_id?: string; agent?: string; actor?: string; attention?: boolean; ref?: string;
}
export interface Page<T> { items: T[]; next_after?: number; has_more?: boolean; [k: string]: unknown }

export interface Question { id: string; type: string; question: string; options?: string[] }
export interface Message {
  id: string; project_id: string; task_id?: string; sender: string; recipient: string; conversation_id: string;
  message_type: string; body: string; questions?: Question[]; answers?: Record<string, unknown>; in_reply_to?: string;
  blocking: boolean; status: string; version: number; created_at: number; deadline?: number; delivery_status: string;
  escalation_reason?: string; category?: string; approval_id?: string; report_id?: string; decision_id?: string;
  may_be_outdated?: boolean;
}

export interface Approval {
  id: string; project_id: string; agent: string; task_id?: string; run_id: string; message_id: string; kind?: string;
  artifact?: Artifact; call: { name: string; arguments?: Record<string, unknown> }; reason: string; status: string;
  version: number; created_at: number; expires_at: number; decided_at?: number; decision_reason?: string;
}

export interface Comment { id: string; author: string; text: string; created_at: number }
export interface PRFacts { number: number; state: string; head_sha: string; url?: string }
export interface ExitFacts { head: string; dirty: boolean; branch: string; default_branch: string; ahead_of_default: number; pushed: boolean; pr?: PRFacts }
export interface TaskExit { run_id: string; kind: string; facts?: ExitFacts; created_at: number }
export interface TaskPark { id: string; kind: string; ref_id?: string; reason: string; who: string; status: string; run_id?: string; created_at: number; cleared_at?: number }

export interface Overview {
  health: Health; status: ProjectStatus[]; agents: Agent[]; tasks: Task[]; pauses: Pause[]; pending: PendingCounts;
  nodes: FactoryNode[]; costs_today: CostReport; task_costs: CostReport; recent_costs: CostReport;
  live_runs?: Record<string, Run>; inbox: Message[]; approvals: Approval[]; at: string; errors?: Record<string, string>;
}

export interface TaskDetail {
  task: Task; why: Verdict; attempts: Run[]; comments: Comment[]; costs: CostReport; events: FeedItem[];
  children?: Task[]; exits?: TaskExit[]; parks?: TaskPark[]; queued?: QueuedReason;
}

// An owner action (internal/tui.Action; Go field names, no json tags). Kind is one of: pause, resume, retry,
// run, cancel, reassign, reprioritize, comment, direct, answer, approve, reject, stop, start, say, create,
// hold, release, read.
export interface Action {
  Kind: string; TaskID?: string; Project?: string; Title?: string; Deps?: string[]; Ready?: boolean; Version?: number;
  Text?: string; Assignee?: string; Priority?: number; Urgent?: boolean; Cascade?: boolean; MessageID?: string;
  Answers?: Record<string, unknown>; ApprovalID?: string; Scope?: string; Target?: string;
}

export interface Meta { describe: string; mode: "live" | "demo" }
