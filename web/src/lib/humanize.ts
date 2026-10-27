import { format } from "date-fns";
import { displayId } from "./format";

// Core explains why a task does not run as a CLI-shaped verdict (internal/tasks/state.go Evaluate and park):
// a reason, a detail with raw IDs and statuses, and a clearer {who, action} whose action is often a
// `factory …` command with a VERSION placeholder. humanize turns that into product language for the UI:
// a short headline, a plain detail, who clears it (linked when it is a run or a task) and the next step
// as a UI action. Nothing it returns contains a CLI command, a placeholder or a 32-hex ID.

export interface TaskRef { title: string; status?: string }
// Lookup resolves task IDs to titles (from the overview); unknown IDs show as short IDs.
export type Lookup = (id: string) => TaskRef | undefined;
export const lookupFrom = (tasks?: { id: string; title: string; status?: string }[]): Lookup => {
  const m = new Map((tasks ?? []).map((t) => [t.id, { title: t.title, status: t.status }]));
  return (id) => m.get(id);
};

export interface Party {
  label: string;
  href?: string;
  agent?: string; // an agent name, for an avatar
  you?: boolean;
}

export interface HumanWhy {
  headline: string; // "Waiting for 3 subtasks"
  label: string; // the headline without task titles, for tight lists ("Waiting for a dependency")
  brief?: string; // a few words for a card next to the headline
  detail?: string; // a sentence for the task page
  who?: Party; // who or what clears it (absent when time or the scheduler does)
  next?: string; // the next step, as a UI action or what happens on its own
  you: boolean; // it waits on the owner now
  live: boolean; // a run is working on it
  queued: boolean; // a run waits for a node
}

export interface Verdictish {
  reason: string;
  detail?: string;
  clearer?: { who: string; action: string };
  status?: string;
}

const HEX = /\b[0-9a-f]{20,}\b/g;
// A `factory …` command: the binary, its subcommand words, then arguments that look like IDs, placeholders
// (VERSION, EVIDENCE), choices (accept|reject) or flags.
const CLI = /`?\bfactory(?:-node)? (?:(?:tasks|runs|artifacts|openspec|messages|nodes) [a-z][\w-]*|answer|comment|resume|pause|stop|start|run|retry|doctor|web|status)(?: (?:[0-9a-f]{8,}|[A-Z][A-Z_]*|\S*\|\S*|[\w-]*\d[\w-]*|--?[a-z][\w-]*|\.\.\.|now))*`?/g;
const isHex = (s: string) => /^[0-9a-f]{20,}$/.test(s);

// statusWords: a task or run status in words ("IN_PROGRESS" → "in progress").
export const statusWords = (s?: string) =>
  ({ IN_PROGRESS: "in progress", IN_REVIEW: "in review", READY: "ready", BACKLOG: "in the backlog", REWORK: "in rework", BLOCKED: "blocked",
    DONE: "done", CANCELLED: "cancelled", RUNNING: "running", QUEUED: "queued", CLAIMED: "starting", CANCELLING: "stopping", LOST: "out of contact" } as Record<string, string>)[s ?? ""]
  ?? (s ?? "").toLowerCase().replace(/_/g, " ");

const quote = (t: string) => `“${t.length > 60 ? `${t.slice(0, 57).trimEnd()}…` : t}”`;

// taskName: a task's title when known, else its short ID.
export const taskName = (id: string, look?: Lookup, quoted = false) => {
  const t = look?.(id)?.title;
  return t ? (quoted ? quote(t) : t) : isHex(id) ? `task ${displayId(id)}` : id;
};

// cleanText makes free text (an agent's note, a summary, a message body) readable: CLI commands and VERSION
// placeholders go, and long hex IDs become the referenced task's title or a short ID.
export function cleanText(s: string | undefined, look?: Lookup): string {
  if (!s) return "";
  return s
    .replace(/\s*\((?:or |then |e\.g\. )?factory [^()]*(?:\([^()]*\)[^()]*)*\)/g, "")
    .replace(CLI, "")
    .replace(/\bVERSION\b/g, "")
    .replace(HEX, (id) => { const t = look?.(id)?.title; return t ? quote(t) : displayId(id); })
    .replace(/\s{2,}/g, " ")
    .replace(/\s+([,.;:)])/g, "$1")
    .trim();
}

const agentList = (names: string[]) =>
  names.length <= 2 ? names.join(" and ") : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;

// party: a clearer's who in words — "You", the current run, a task's title, agent names or a node.
export function party(who: string | undefined, look?: Lookup): Party | undefined {
  if (!who || who === "nobody" || who === "time" || who === "scheduler") return undefined;
  if (who === "owner") return { label: "You", you: true };
  if (who.startsWith("run:")) return { label: "The current run", href: `/runs/${encodeURIComponent(who.slice(4))}` };
  if (who.startsWith("task:")) { const id = who.slice(5); return { label: taskName(id, look), href: `/tasks/${encodeURIComponent(id)}` }; }
  if (who.startsWith("node")) return { label: "Its node", href: "/nodes" };
  if (who === "children") return { label: "Its subtasks" };
  if (who === "reviewer") return { label: "The reviewer" };
  const agents = who.split(",").map((w) => w.trim().replace(/^agent:/, "")).filter(Boolean);
  if (agents.length === 0) return undefined;
  return { label: agentList(agents), agent: agents.length === 1 ? agents[0] : undefined, href: agents.length === 1 ? `/tasks?assignee=${encodeURIComponent(agents[0])}` : undefined };
}

// release is the UI action that releases a task (Core's `tasks ready`): Run now from the backlog or Ready,
// Retry otherwise (task-actions.tsx).
const release = (status?: string) => (status === "BACKLOG" || status === "READY" ? "Run now" : "Retry");

const when = (t: Date, now?: number) => {
  if (Number.isNaN(t.getTime())) return "";
  if (now) {
    const s = Math.round(t.getTime() / 1000 - now);
    if (s <= 45) return "any moment";
    if (s < 3600) return `in ${Math.round(s / 60)} min`;
    if (s < 86400) return `in ${Math.round(s / 3600)} h`;
  }
  return `at ${format(t, now && Math.abs(t.getTime() / 1000 - now) < 86400 ? "HH:mm" : "MMM d, HH:mm")}`;
};

const scopeWords = (scope: string) => (scope === "quota_key" ? "subscription" : scope.replace(/_/g, " "));

// humanize explains a verdict (or a task's reason and clearer) in product language. now (Unix seconds)
// makes backoff times relative.
export function humanize(v: Verdictish, look?: Lookup, now?: number): HumanWhy {
  const reason = v.reason ?? "";
  const who = v.clearer?.who ?? "";
  const action = v.clearer?.action ?? "";
  const detail = v.detail ?? "";
  const p = party(who, look);
  const base: HumanWhy = { headline: "", label: "", who: p, you: who === "owner", live: false, queued: false };
  const out = (h: Partial<HumanWhy> & { headline: string }): HumanWhy => ({ ...base, label: h.headline, ...h });
  const note = capital(cleanText(detail, look)) || undefined;

  if (reason === "terminal" || v.status === "DONE" || v.status === "CANCELLED") {
    return out({ headline: v.status === "CANCELLED" ? "Cancelled" : v.status === "DONE" ? "Done" : "Closed", who: undefined, you: false });
  }
  if (reason === "backlog") {
    return out({ headline: "In backlog", brief: "Not started yet", detail: "It is not released to the team yet.", next: "Start it with Run now when it should be worked on.", you: false });
  }
  if (reason === "unassigned") {
    const stray = /^assignee (\S+) is not a configured agent/.exec(detail)?.[1];
    return stray
      ? out({ headline: "Assignee is not on the team", brief: "Reassign it", detail: `${stray} is not one of your agents.`, next: "Reassign it to an agent from the task menu." })
      : out({ headline: "No assignee", brief: "Assign an agent", detail: "Nobody is assigned to work on it.", next: "Assign an agent with Reassign in the task menu." });
  }
  if (reason === "live_run") {
    const st = /\bis (QUEUED|CLAIMED|RUNNING|CANCELLING|LOST)\b|\bis (lost)\b/.exec(detail);
    const s = (st?.[1] ?? st?.[2] ?? "RUNNING").toUpperCase();
    const runId = /\brun ([0-9a-f]{20,}|\S+)/.exec(detail)?.[1] ?? (who.startsWith("run:") ? who.slice(4) : "");
    const run: Party | undefined = runId ? { label: "The current run", href: `/runs/${encodeURIComponent(runId)}` } : p;
    if (s === "LOST") {
      return out({ headline: "Run out of contact", brief: "Waiting for its node", detail: "Its node stopped reporting; the run may still be working.", who: run,
        next: "It continues when the node reports back. You can decide what to do from the run page." });
    }
    if (s === "QUEUED" || s === "CLAIMED") {
      return out({ headline: s === "QUEUED" ? "Waiting for a node" : "Starting", brief: s === "QUEUED" ? "Queued" : undefined, detail: s === "QUEUED" ? "A run is queued and starts when a node picks it up." : "A node picked up the run and is starting it.", who: run, queued: s === "QUEUED", live: s === "CLAIMED" });
    }
    return out({ headline: s === "CANCELLING" ? "Stopping" : "Running", detail: s === "CANCELLING" ? "The current run is stopping." : "An agent is working on it now.", who: run, live: true,
      next: s === "CANCELLING" ? undefined : "Follow it live on the run page." });
  }
  if (reason.startsWith("parked:")) {
    const kind = reason.slice(7);
    switch (kind) {
      case "children": {
        const list = /^wait for (.+?) to close/.exec(action)?.[1] ?? "";
        const ids = list && list !== "its open children" ? list.split(/,\s*/) : [];
        const n = ids.length;
        const open = n ? `${n} subtask${n === 1 ? "" : "s"}` : "its subtasks";
        const head = n === 1 ? "Waiting for a subtask" : `Waiting for ${open}`;
        return out({ headline: head, brief: p && !p.you ? `with ${p.label}` : undefined, detail: note,
          next: `It continues when ${n === 1 ? "the subtask closes" : "they close"}. ${release(v.status)} in the task menu releases it early.` });
      }
      case "escalation":
        return out({ headline: base.you ? "Escalated to you" : `Escalated to ${p?.label ?? "a lead"}`, brief: base.you ? "Answer in the inbox" : undefined, detail: note,
          next: base.you ? "Answer the escalation in your inbox." : "It continues once the escalation is answered." });
      case "inspect":
        return out({ headline: "Needs inspection", brief: base.you ? "Check its last run" : undefined, detail: note,
          next: "Check the workspace of its last run, then continue, retry or cancel it from the run page." });
      case "ask":
        return out({ headline: base.you ? "Waiting for your answer" : `Waiting for an answer${p ? ` from ${p.label}` : ""}`, brief: base.you ? "Answer in the inbox" : undefined, detail: note,
          next: base.you ? "Answer the question in your inbox." : "It continues once the question is answered." });
      case "block":
        if (who === "node") return out({ headline: "Resetting its workspace", detail: note, next: "Its node resets the work from the remote branch; it runs again after that." });
        return out({ headline: "Blocked", brief: base.you ? `Fix, then ${release(v.status)}` : undefined, detail: note,
          next: `Remove the cause, then use ${release(v.status)} in the task menu.` });
      case "wait":
        return out({ headline: "Sleeping", brief: "Wakes on its own", detail: note, who: undefined,
          next: "It wakes at its time; a comment or a directive wakes it now." });
      default:
        return out({ headline: "On hold", detail: note, next: cleanText(action, look) || undefined });
    }
  }
  if (reason === "dependencies") {
    const id = who.startsWith("task:") ? who.slice(5) : /^dependency (\S+)/.exec(detail)?.[1] ?? "";
    const st = /\bis ([A-Z_]+)$/.exec(detail)?.[1] ?? look?.(id)?.status;
    const name = id ? taskName(id, look, true) : "another task";
    return out({ headline: `Waiting for ${name}`, label: "Waiting for a dependency", brief: st ? `it is ${statusWords(st)}` : undefined,
      detail: id ? `${taskName(id, look, true)} has to be done first${st ? `; it is ${statusWords(st)}` : ""}.` : "Another task has to be done first.",
      next: "It starts on its own when that task is done." });
  }
  if (reason === "artifacts") {
    return out({ headline: "Waiting for your approval", brief: "Approve in the inbox", detail: "A file it needs has not been approved yet.", next: "Approve it in your inbox." });
  }
  if (reason.startsWith("paused:")) {
    const scope = reason.slice(7);
    if (who === "time") {
      return out({ headline: "Paused: usage limit reached", brief: "Resumes on its own", detail: "The subscription limit is reached.", next: "It resumes when the limit resets.", who: undefined });
    }
    if (scope === "task") {
      const actor = /^paused by (\S+?)(?::|$)/.exec(detail)?.[1] ?? who;
      const why = /^paused by \S+?: (.+)$/.exec(detail)?.[1];
      const byYou = actor === "owner";
      const why2 = why && !/^paused by owner$/i.test(why) ? cleanText(why, look) : undefined;
      return out({ headline: byYou ? "Paused by you" : `Paused by ${actor}`, brief: byYou ? "Resume it" : undefined,
        detail: why2 && capital(why2), who: byYou ? { label: "You", you: true } : party(actor, look),
        next: byYou ? "Resume it from the task menu." : `It waits until ${actor} or you resume it from the task menu.` });
    }
    return out({ headline: `Paused: ${scopeWords(scope)}`, brief: "Resume on the Nodes page", detail: `The whole ${scopeWords(scope)} is paused, so no new runs start.`,
      next: "Resume it on the Nodes page." });
  }
  if (reason === "backoff") {
    const at = /after (\d{4}-\d\d-\d\dT[\d:]+Z)/.exec(detail)?.[1];
    const fails = /\((\d+) failed in a row/.exec(detail)?.[1];
    const w = at ? when(new Date(at), now) : "";
    return out({ headline: w ? `Retrying ${w}` : "Retrying soon", brief: fails ? `${fails} failed attempt${fails === "1" ? "" : "s"}` : undefined,
      detail: fails ? `The last ${fails === "1" ? "attempt" : `${fails} attempts`} failed, so it waits before trying again.` : "It waits before trying again.",
      next: `It retries on its own; ${release(v.status)} in the task menu starts it now.`, who: undefined, you: false });
  }
  if (reason === "openspec") {
    return out({ headline: "Review on hold", brief: "OpenSpec change missing", detail: `${note ? `${note.replace(/\s*\(the review run is not dispatched\)/, "")}. ` : ""}Its review cannot start.`,
      next: "Restore its OpenSpec change, or review the work yourself." });
  }
  if (reason === "idle") {
    if (/waiting for review/.test(detail) || who === "reviewer") {
      return out({ headline: "Waiting for review", detail: "The work is submitted and waits for a reviewer.", next: "The reviewer accepts it or sends it back for rework." });
    }
    if (/review rejected/.test(detail)) {
      return out({ headline: "Rework not started", brief: "Retry to start rework", detail: "The review sent it back; the rework is not released yet.", next: "Start the rework with Retry in the task menu." });
    }
    if (/without a live run/.test(detail)) {
      return out({ headline: "Stalled", brief: "Check the last attempt", detail: "It is in progress, but no run is working on it.", next: "Check its last attempt, then use Retry in the task menu." });
    }
    if (v.status === "BLOCKED" || (detail && !/no unhandled trigger/.test(detail))) {
      return out({ headline: "Blocked", brief: `Fix, then ${release(v.status)}`, detail: note, next: `Remove the cause, then use ${release(v.status)} in the task menu.` });
    }
    return out({ headline: "Idle", brief: "Nothing to do yet", detail: "It has nothing new to act on.", next: `It wakes on a comment or a directive; ${release(v.status)} in the task menu starts it now.`, you: false });
  }
  if (reason === "pinned_node_unavailable") {
    return out({ headline: "Its node is offline", brief: "Answer in the inbox", detail: "The node with its unsaved work is unavailable.",
      next: "Bring the node back, or answer the escalation in your inbox to continue on another node." });
  }
  if (reason === "no_capable_node") {
    return out({ headline: "No node can run it", brief: "Start a capable node", detail: note ? `${capital(note)}.` : "No online node has what its agent needs.",
      next: "Start or fix a node with what this agent needs." });
  }
  if (reason === "capacity") {
    const agent = /^agent (\S+) is busy/.exec(detail)?.[1];
    const other = /\(task (\S+)\)/.exec(detail)?.[1];
    const runId = who.startsWith("run:") ? who.slice(4) : "";
    const blocker: Party | undefined = other ? { label: taskName(other, look), href: `/tasks/${encodeURIComponent(other)}` } : runId ? { label: "Another run", href: `/runs/${encodeURIComponent(runId)}` } : p;
    if (agent) return out({ headline: "Agent busy", brief: `${agent} is on another task`, detail: `${agent} is working on ${other ? taskName(other, look, true) : "another task"}.`, who: blocker, next: "It starts when the agent is free." });
    if (/writer slot/.test(detail)) return out({ headline: "Project busy", brief: "Another run uses the project", detail: `${other ? taskName(other, look, true) : "Another task"} is using the project's workspace.`, who: blocker, next: "It starts when that run ends." });
    if (/unpushed work is on node/.test(detail)) return out({ headline: "Waiting for its node", detail: "Its unsaved work is on a node that is busy or offline.", who: { label: "Its node", href: "/nodes" }, next: "It continues on that node when it is back." });
    return out({ headline: "All nodes busy", brief: "Starts when a node frees up", detail: "Every node that can run it is full.", who: undefined, next: "It starts when a run on a capable node ends." });
  }
  if (reason === "project_setup") {
    // "the setup of project P's repository failed (REASON); task T retries it" or
    // "project P's repository is being set up by task T's run" (storage setupWait).
    const failed = /repository failed \((.*)\); task (\S+) retries it/.exec(detail);
    const by = failed?.[2] ?? /set up by task (\S+?)'s run/.exec(detail)?.[1];
    const setter: Party | undefined = by ? { label: taskName(by, look), href: `/tasks/${encodeURIComponent(by)}` } : undefined;
    if (failed) {
      return out({ headline: "Repository setup failed", label: "Waiting for the repository", brief: "Setup failed", who: setter,
        detail: `The project's repository could not be set up: ${cleanText(failed[1], look)}.`,
        next: `Fix the cause on the node, then retry ${by ? taskName(by, look, true) : "the task that sets it up"}; it starts after that.`, you: false });
    }
    return out({ headline: "Waiting for the repository", brief: by ? "Being set up" : undefined, who: setter,
      detail: by ? `${taskName(by, look, true)} sets up the project's repository first.` : "The project's first task sets up its repository first.",
      next: "It starts on its own once the repository is ready.", you: false });
  }
  if (reason === "runnable") return out({ headline: "Starting soon", detail: "It is next in line to run.", who: undefined, you: false });
  return out({ headline: capital(reason.replace(/[:_]/g, " ")), detail: note, next: cleanText(action, look) || undefined });
}

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

// triggerWords: a pending trigger in words ("a comment by you", "a finished dependency").
const triggerKinds: Record<string, string> = {
  ready: "a release", comment: "a comment", directive: "a directive", dependency_done: "a finished dependency", artifact_approved: "an artifact approval",
  answer: "an answer", review_rejected: "review feedback", review_accepted: "an accepted review", child_done: "a finished subtask", wake: "a wake-up",
  reset_done: "a workspace reset", retry: "a retry",
};
export function triggerWords(t: { kind: string; actor: string }): string {
  const k = triggerKinds[t.kind] ?? `a ${t.kind.replace(/_/g, " ")}`;
  if (!t.actor || ["system", "scheduler", "core", "factory"].includes(t.actor)) return k;
  return `${k} by ${t.actor === "owner" ? "you" : t.actor.replace(/^agent:/, "")}`;
}
