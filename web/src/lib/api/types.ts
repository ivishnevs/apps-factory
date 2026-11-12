// JSON shapes of the owner API (`factory web`, internal/web), mirroring the Go structs' json tags
// (internal/tui, internal/storage, internal/tasks, internal/messaging, internal/approvals, internal/core).
// Times named *_at are Unix seconds unless typed string (RFC 3339).

export interface Clearer { who: string; action: string }
export interface Trigger { seq: number; kind: string; actor: string; authority: boolean; ref_id?: string }
// Wait is a capacity verdict in structured form (internal/tasks Wait): the agent's limit, the project workspace,
// the task's node or full nodes.
export interface Wait {
  kind: "agent" | "agent_project" | "agent_conversation" | "writer" | "pinned_node" | "nodes"; agent?: string; live?: number; limit?: number;
  task_id?: string; run_id?: string; node?: string; lost?: boolean;
}
// Tie (node-ties): why a task or a queued run waits for one node — its unsaved work (work) or its agent's
// session (session) is there — since when and how it ends (release; release_at for a timed session tie).
export interface Tie {
  node: string; node_name?: string; reason: "work" | "session"; since?: number; release: string; release_at?: number; unsaved?: string;
}
// Awaits (decision-waits): the decision or message a task — or the dependency it waits for (task_id, the root) — is
// parked on. who is the decision's addressee or the awaited sender ("owner" or "agent:NAME"); message_id is the
// decision's Inbox notice.
export interface Awaits {
  kind: "decision" | "message"; decision_id?: string; message_id?: string; from?: string; topic?: string; who: string; deadline?: number; task_id?: string;
}
export interface Verdict {
  task_id: string; status: string; reason: string; detail?: string; clearer: Clearer; pending_triggers?: Trigger[]; wait?: Wait; tie?: Tie; awaits?: Awaits;
}

export interface Artifact { id: string; version: string; uri: string; size?: number; name?: string; sha256?: string }
export interface Submission { summary: string; commit?: string; checks?: string[]; artifacts?: Artifact[]; pull_request?: { number?: number; url?: string } }

export type TaskStatus = "BACKLOG" | "READY" | "IN_PROGRESS" | "IN_REVIEW" | "REWORK" | "BLOCKED" | "DONE" | "CANCELLED";

export interface Task {
  id: string; project_id: string; title: string; description: string; status: TaskStatus; priority: number;
  assignee: string; created_by: string; parent_task_id?: string; openspec_change?: string; dependencies: string[];
  created_at: number; updated_at: number; version: number; current_run_id?: string; attempt: number; result?: string;
  submission?: Submission; review_feedback?: string; block_reason?: string; cancel_requested: boolean;
  origin_task?: string; origin_run?: string; attempts_no_progress?: number; fail_streak?: number; not_before?: number;
  progress_at?: number; implementer?: string; reviewer?: string; review_by?: string; created_by_person?: string; blocked_on?: string[]; model?: string;
  required_artifacts?: Artifact[]; wait_message_id?: string; kind?: string; paused_by?: string;
  // task-duplicate-warnings: the work it covers (inferred from its text when it named none), the tasks it replaces
  // or was replaced by, and the unanswered duplicate warnings with another open task.
  covers?: string[]; covers_inferred?: boolean; replaces?: string[]; replaced_by?: string[];
  not_duplicate_of?: string[]; possible_duplicates?: DuplicateWarning[];
  // Added by the overview: the why reason, who clears it and the task's tie to a node.
  reason: string; clearer: Clearer; wait?: Wait; tie?: Tie; awaits?: Awaits;
}

// DuplicateWarning is tasks.DuplicateWarning: another open task that may cover the same work, and why.
export interface DuplicateWarning { task_id: string; title: string; status: string; assignee?: string; reasons: string[]; keys?: string[] }

export interface Agent { name: string; role?: string; reports_to?: string; provider?: string; model?: string }

export interface Project {
  // team: the team of the company that works on it (company-teams).
  id: string; name: string; team?: string; description: string; workspace: string; version: number; created_at: number;
  updated_at: number; repo_url?: string; default_branch?: string; rules?: string; direction?: string; archived?: boolean;
  approvals?: import("./types-catalog").ApprovalModes;
  // project-creation: how the repository came to be, who created the project (an agent's request carries its
  // approval) and its setup on the node; workspace is "" until the setup is ready. No setup: nothing to set up.
  repo_mode?: RepoMode; created_by?: string; approval_id?: string; setup?: ProjectSetup;
  // artifact-retention: superseded artifact versions older than this many days are archived; 0 or absent keeps them.
  artifact_retention_days?: number;
}

export type SetupState = "pending" | "running" | "ready" | "failed";
// ProjectSetup is tasks.ProjectSetup: the node that runs the project's first task sets the repository up.
export interface ProjectSetup {
  state: SetupState; task_id?: string; run_id?: string; node_id?: string; reason?: string;
  audit?: boolean; audit_task_id?: string; updated_at?: number;
}

// holder and inactive (affinity): the node holding the session and why it is inactive; such a session can be released (node-readiness). tie: node-ties.
export interface QueuedReason { reason: string; detail: string; until?: number; nodes?: unknown[]; holder?: string; inactive?: string; tie?: Tie; subject?: string }
// Bottleneck (throughput-signals): one cause of queued work while node capacity is idle or unusable, from the stall
// sweep over the queued reasons and the dependency graph (GET /throughput). due: past the company's threshold (the
// manager has the notice, the owner the alert).
export interface Bottleneck {
  project: string; key: string; reason: string; subject?: string; summary: string; fix: string; count: number;
  tasks: string[]; runs?: string[]; free_slots: number; since?: number; due?: boolean;
}
export interface RunStatus { id: string; agent: string; task_id?: string; status: string; started_at?: number; queued?: QueuedReason }
export interface ProjectStatus {
  project: Project; runs: RunStatus[]; tasks: Record<string, number>; waiting: Verdict[]; runnable: number; inbox: number;
}

export interface Pause { scope: string; target?: string; reason?: string; actor: string; created_at: number; until?: number }
export interface PendingCounts { inbox: number; approvals: number; alerts: number; inspect: number; in_review: number; owner_reviews?: number; total: number }

// ActiveAlert is one item of the Inbox's Alerts section (storage.ActiveAlert, GET /alerts; inbox-badge-parity): an
// active task alert of any kind, or a run to inspect (kind inspect). with: the lead expected to act ("" the owner).
export interface ActiveAlert {
  id: string; kind: string; task_id: string; task_title: string; project_id: string; assignee?: string; ref_id?: string;
  summary: string; since: number; action?: string; with?: string;
}

export interface Health { status: string; database?: string; company: string; explicit_exits?: string; default_report?: string }

// conversation_slots: runs in the node's conversation slots; conversation_runs: its live conversation runs (work-and-conversation-runs).
export interface NodeUse { runs: number; resources: Record<string, number>; conversation_slots?: number; conversation_runs?: number }
// AgentRuns: an agent's live and queued runs per kind (GET /agents/runs).
export interface AgentRuns { work_live: number; work_queued: number; conversation_live: number; conversation_queued: number }
// NodeTied (sticky-work-first, node-ties): open tasks whose unsaved work is only on the node (work), and queued runs
// only it may take now. pinned and TiedRun.tie are the old names, kept for one release.
export interface TiedRun { run_id: string; task_id: string; agent: string; reason?: "work" | "session"; tie: "pinned" | "session" }
export interface NodeTied { work?: string[]; pinned: string[]; waiting: TiedRun[] }
// node-readiness (storage.ProjectReadiness): a node's readiness for one project; faults say what failed and the fix.
export interface NodeFault { kind: string; detail: string; fix?: string }
export interface ProjectReadiness {
  project: string; ready: boolean; faults: NodeFault[]; source: "probe" | "run"; run_id?: string; since?: number; checked_at?: number; pending?: boolean;
}
// storage.NodeSessions: what a node pause holds back (session-affinity leases and the runs waiting for them).
export interface HeldSession { session_id: string; agent: string; task_id?: string; title?: string; released?: boolean }
export interface NodeSessions { held: HeldSession[]; waiting: TiedRun[]; release_seconds: number }
export interface FactoryNode {
  node_id: string; name: string; labels?: string[]; slots?: number; conversation_slots?: number; drivers?: { name: string; ok: boolean; [k: string]: unknown }[];
  health: Record<string, unknown>; registered_at: number; last_seen: number; online: boolean; in_use: NodeUse; tied?: NodeTied;
  readiness?: ProjectReadiness[];
  // node-packaging-updates: the package version, below Core's min_node_version no work; labels the node detected
  // (macos/linux, arm64/amd64, xcode, ios-sim, headless, gpu) next to the owner's; the settings from the web.
  version?: string; update_required?: boolean; min_version?: string; detected_labels?: string[] | null;
  config?: NodeConfigView;
  [k: string]: unknown;
}
// protocol.NodeSettings: the settings from the web (an absent one keeps node.yaml's).
export interface NodeSettings {
  slots?: number; conversation_slots?: number; labels?: string[];
  drivers?: Record<string, { enabled?: boolean }>;
  disk?: { low_watermark_gib?: number; critical_watermark_gib?: number; conversation_floor_gib?: number };
}
export interface NodeSettingState { state: "applied" | "pending" | "refused"; reason?: string }
// storage.NodeConfigView: desired settings and version, the node's report (local: node.yaml's values) and each state.
export interface NodeConfigView {
  version: number; desired: NodeSettings; updated_by?: string; updated_at?: number; states: Record<string, NodeSettingState>;
  report?: { version: number; settings: Record<string, NodeSettingState>; local: NodeSettings; pinned?: string[]; slots_max?: number };
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
  usage?: Usage[]; outcome?: string; node_id?: string;
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
  status?: string; summary?: string; task_id?: string; run_id?: string; agent?: string; actor?: string; attention?: boolean; ref?: string; person?: string;
}
export interface Page<T> { items: T[]; next_after?: number; has_more?: boolean; [k: string]: unknown }

export interface Question { id: string; type: string; question: string; options?: string[] }
export interface Message {
  id: string; project_id: string; task_id?: string; sender: string; recipient: string; conversation_id: string; person?: string; person_id?: string;
  message_type: string; body: string; questions?: Question[]; answers?: Record<string, unknown>; in_reply_to?: string;
  blocking: boolean; status: string; version: number; created_at: number; deadline?: number; delivery_status: string;
  escalation_reason?: string; category?: string; approval_id?: string; report_id?: string; decision_id?: string;
  may_be_outdated?: boolean;
  // inbox-badge-parity: "promised" (the owner will send details; still open), "sent" or "withdrawn".
  details?: string; details_note?: string; details_at?: number;
  // Files the sender attached and references to existing materials (inbox-visual-previews), in the order to show.
  attachments?: MessageAttachment[]; materials?: MaterialRef[];
}

// A preview is a display-size rendition of a large image; the original stays downloadable.
export interface ImagePreview { sha256: string; size: number; width: number; height: number }
export interface MessageAttachment { path: string; caption?: string; sha256: string; size: number; redacted?: boolean; preview?: ImagePreview }
// A reference to an artifact version (artifact_id, artifact_version) or a report attachment (report_id, path), resolved by Core.
export interface MaterialRef {
  artifact_id?: string; artifact_version?: string; report_id?: string; path?: string; caption?: string;
  name?: string; sha256?: string; size?: number; uri?: string; redacted?: boolean; preview?: ImagePreview;
}

export interface Approval {
  id: string; project_id: string; agent: string; task_id?: string; run_id: string; message_id: string; kind?: string;
  artifact?: Artifact; call: { name: string; arguments?: Record<string, unknown> }; reason: string; status: string;
  version: number; created_at: number; expires_at: number; decided_at?: number; decision_reason?: string;
  audience?: string; delegate?: string; setting?: string; escalated?: boolean; decided_by?: string; cites?: string[];
}

// A board comment's handling (owner-comments-handled): one entry per agent that got it; status open is "not handled yet".
export interface CommentOutcome { agent: string; reason: string; status: "open" | "done" | "forwarded" | "declined"; note?: string; to?: string; task?: string; at?: number }
export interface Comment { id: string; author: string; text: string; created_at: number; person?: string; category?: string; outcomes?: CommentOutcome[] }
export interface PRFacts { number: number; state: string; head_sha: string; url?: string }
export interface ExitFacts { head: string; dirty: boolean; branch: string; default_branch: string; ahead_of_default: number; pushed: boolean; pr?: PRFacts }
export interface TaskExit { run_id: string; kind: string; facts?: ExitFacts; created_at: number }
export interface TaskPark { id: string; kind: string; ref_id?: string; reason: string; who: string; status: string; run_id?: string; created_at: number; cleared_at?: number }

export interface Overview {
  health: Health; status: ProjectStatus[]; agents: Agent[]; tasks: Task[]; pauses: Pause[]; pending: PendingCounts;
  nodes: FactoryNode[]; costs_today: CostReport; task_costs: CostReport; recent_costs: CostReport;
  live_runs?: Record<string, Run>; inbox: Message[]; approvals: Approval[]; alerts?: ActiveAlert[]; at: string; errors?: Record<string, string>;
  throughput?: Bottleneck[];
}

export interface TaskDetail {
  task: Task; why: Verdict; attempts: Run[]; comments: Comment[]; costs: CostReport; events: FeedItem[];
  children?: Task[]; exits?: TaskExit[]; parks?: TaskPark[]; queued?: QueuedReason;
}

// An owner action (internal/tui.Action; Go field names, no json tags). Kind is one of: pause, resume, retry,
// run, cancel, reassign, reprioritize, comment, reopen, ask_lead, direct, answer, approve, reject, stop, start, say, create,
// create_project (NewProject is the POST /projects body), hold, release, read.
export interface Action {
  Kind: string; TaskID?: string; Project?: string; Title?: string; Deps?: string[]; Ready?: boolean; Version?: number;
  Text?: string; Assignee?: string; ReviewBy?: string; Priority?: number; Urgent?: boolean; Cascade?: boolean; MessageID?: string;
  Answers?: Record<string, unknown>; ApprovalID?: string; Scope?: string; Target?: string; PullRequest?: string; Merged?: boolean;
  NewProject?: ProjectCreate; DecisionID?: string; Status?: string; Value?: string;
  Details?: "promised" | "withdrawn"; // answer: promise the details, or withdraw that promise (inbox-badge-parity)
}

// How Core provisions a new project's repository (project-creation).
export type RepoMode = "local" | "github" | "existing";

// ProjectCreate is tasks.ProjectCreate, the POST /projects body; request_id is one per submit.
export interface ProjectCreate {
  id: string; name: string; team?: string; description: string; workspace?: string; repo_url?: string; default_branch?: string;
  request_id: string; repo_mode: RepoMode; audit?: boolean;
}

export interface Meta { describe: string; mode: "live" | "demo" }

// A project decision (internal/decisions Decision): proposed, under_discussion, approved, rejected or superseded.
export interface Decision {
  id: string; project_id: string; category?: string; topic: string; status: string; value: string; alternatives?: string[];
  rationale: string; created_by: string; approved_by?: string; review_rationale?: string; version: number; task_id?: string;
  message_id?: string; created_at: number; updated_at: number;
  // The open tasks parked on it (decision-waits).
  waiting?: { task_id: string; title: string }[];
  attachments?: MessageAttachment[]; materials?: MaterialRef[];
}
