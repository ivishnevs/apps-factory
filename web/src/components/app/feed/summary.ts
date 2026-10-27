// feedSummary turns a feed row's machine summary (storage/feed.go and the event writers) into a sentence
// for people: no 32-hex IDs (a known task shows its title, anything else its short displayId or nothing),
// readable times and durations, and a sentence for the kinds that carry no summary.
import { format } from "date-fns";
import type { FeedItem } from "@/lib/api/types";
import { displayId } from "@/lib/format";

const HEX = "[0-9a-f]{32}";
const reHex = (s: string, flags = "g") => new RegExp(s.replaceAll("HEX", HEX), flags);

const outcomes: Record<string, string> = {
  succeeded: "Run succeeded", failed: "Run failed", cancelled: "Run cancelled", canceled: "Run cancelled",
  timeout: "Run timed out", timed_out: "Run timed out", max_turns: "Run hit its turn limit", setup_failed: "Run could not start",
  lost: "Run lost", unclaimed: "Run never started", interrupted: "Run interrupted",
};
const outcomeText = (o: string) => outcomes[o] ?? `Run ended (${o.replace(/_/g, " ")})`;

const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);
const quote = (s: string) => `“${s}”`;

// duration: Go's "1m0s" → "1m", "1h0m0s" → "1h".
const duration = (s: string) => s.replace(/(\d+[hm])0s\b/g, "$1").replace(/(\d+h)0m\b/g, "$1");

// titled: "task <id> (Title (with parens))" and "<id> (Title)" name the task by its title.
function titled(s: string): string {
  const re = reHex("(?:task )?HEX \\(");
  let out = "";
  let m: RegExpExecArray | null;
  let from = 0;
  while ((m = re.exec(s))) {
    let depth = 1;
    let i = m.index + m[0].length;
    for (; i < s.length && depth > 0; i++) depth += s[i] === "(" ? 1 : s[i] === ")" ? -1 : 0;
    if (depth > 0) break;
    out += s.slice(from, m.index) + quote(s.slice(m.index + m[0].length, i - 1));
    from = re.lastIndex = i;
  }
  return out + s.slice(from);
}

// prose cleans IDs, statuses, times and durations out of any summary text.
function prose(text: string, title: (id: string) => string | undefined): string {
  let s = text;
  // Approval answers: "Approval <id> APPROVED: approved by owner" → "Approved".
  s = s.replace(reHex("Approval HEX (APPROVED|REJECTED)(?::\\s*(?:approved|rejected) by (?:the )?owner)?"), (_, st: string) => (st === "APPROVED" ? "Approved" : "Rejected"));
  s = s.replace(reHex("Artifact approval HEX:\\s*"), "Approval request: ");
  s = titled(s);
  // "(approval <id>)", "(<id>)": a parenthesised reference adds nothing.
  s = s.replace(reHex("\\s*\\((?:approval |node |run )?HEX\\)"), "");
  // A reference after a word that already says what it is: "question <id>", "run <id>", "node <id>".
  s = s.replace(reHex("\\b(question|answer|approval|node|run|Runtime) HEX\\b", "gi"), "$1");
  // Anything left: a known task by its title, otherwise its short form.
  s = s.replace(reHex("\\bHEX\\b"), (id) => (title(id) ? quote(title(id)!) : displayId(id)));
  s = s.replace(/\b([a-z][\w-]*)@(\d+)\b/g, "$1 v$2");
  s = s.replace(/^artifact approval of (.+?) (approved|rejected) by the owner(?::\s*(?:approved|rejected) by owner)?/i, (_, a: string, d: string) => `Owner ${d.toLowerCase()} ${a}`);
  s = s.replace(/\bthe answer to question from\b/, "an answer from");
  s = s.replace(/^wait interrupted by (\w+)/i, (_, k: string) => `Wait interrupted by ${k === "approval_decided" ? "an approval decision" : k.replace(/_/g, " ")}`);
  s = s.replace(/\b(DONE|APPROVED|REJECTED|FAILED|CANCELLED|BLOCKED|READY|ESCALATED|PENDING)\b/g, (m) => m.toLowerCase());
  s = s.replace(/\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?Z\b/g, (iso) => {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? iso : format(d, "MMM d, HH:mm");
  });
  s = s.replace(/\b\d+h\d+m\d+s\b|\b\d+m\d+s\b/g, duration);
  return s;
}

// openspecPath: "changes/android/tasks.md" → "tasks.md in change android".
function openspecPath(p: string) {
  const m = p.match(/^changes\/([^/]+)\/(.+)$/);
  if (m) return `${m[2]} in change ${m[1]}`;
  return p;
}

// projectSummary: Core's project.created / provisioned / setup_failed notes (storage createdNote,
// provisionedNote, CompleteProjectSetup) in the owner's words. title("node:<id>") names a node.
function projectSummary(t: string, raw: string, title: (id: string) => string | undefined): string | undefined {
  const who = (w: string) => (w === "owner" ? "you" : w.replace(/^agent:/, ""));
  const node = (id: string) => title(`node:${id}`) ?? (new RegExp(`^${HEX}$`).test(id) ? "" : id);
  const on = (id: string) => (node(id) ? ` on ${node(id)}` : "");
  let m: RegExpMatchArray | null;
  if (t === "project.created" && (m = raw.match(/^project \S+ created by ([^,;]+)(, approved by the owner[^;]*)?(?:;\s*(.*))?$/))) {
    const repo = (m[3] ?? "")
      .replace(/^repository:\s*/, "")
      .replace(/^new local git repository/, "new local repository")
      .replace(/, created (?:on the node|through the node's gh) when the first task runs$/, ", set up when its first task runs")
      .replace(/^existing (.+), onboarded on the node before the first task( \(audit first\))?$/, (_, w: string, a?: string) => `existing repository ${w}, onboarded before its first task${a ? ", audit first" : ""}`);
    return `Created by ${who(m[1].trim())}${m[2] ? ", approved by you" : ""}${repo ? ` · ${cap(repo)}` : ""}`;
  }
  if (t === "project.provisioned" && (m = raw.match(/^project \S+ ready on node (\S+): ([\s\S]*)$/))) {
    const [what, ...rest] = m[2].split("; ");
    const parts = rest.map((p) => p
      .replace(/ into Factory$/, "")
      .replace(/^the repository no longer tracks the OpenSpec tree \((.*)\)$/, "OpenSpec untracked in git: $1"));
    return [`Repository ready${on(m[1])}: ${what.replace(/^local git repository /, "local repository ")}`, ...parts].join(" · ");
  }
  if (t === "project.setup_failed" && (m = raw.match(/^setting up project \S+'s repository failed on node (\S+): ([\s\S]*)$/))) {
    return `Repository setup failed${on(m[1])}: ${prose(m[2], title)}`;
  }
  return undefined;
}

export function feedSummary(item: FeedItem, title: (id: string) => string | undefined = () => undefined): string {
  const raw = (item.summary ?? "").trim();
  const t = item.type;
  const taskTitle = item.task_id ? title(item.task_id) : undefined;
  let m: RegExpMatchArray | null;

  if (t === "report.created" && (m = raw.match(reHex("^Runtime HEX ended (\\S+)([\\s\\S]*)$", "")))) {
    return outcomeText(m[1]) + prose(m[2], title);
  }
  if (t === "run.unclaimed" && (m = raw.match(reHex("^run HEX ended unclaimed before it started:\\s*(?:unclaimed:\\s*)?([\\s\\S]*)$", "")))) {
    return `Run never started: ${prose(m[1], title)}`;
  }
  if (t === "task.retry" && (m = raw.match(reHex("^[^:]*: run HEX ended (\\S+); retry in (\\S+?)(?::\\s*([\\s\\S]*))?$", "")))) {
    return `${outcomeText(m[1])}, retrying in ${duration(m[2])}` + (m[3] ? `: ${prose(m[3], title)}` : "");
  }
  if (t === "owner.paused" || t === "owner.resumed") {
    if ((m = raw.match(reHex("^node HEX(?::\\s*(?:--reason\\s+)?([\\s\\S]*))?$", "")))) {
      return (t === "owner.paused" ? "Paused a node" : "Resumed a node") + (m[1] ? `: ${prose(m[1], title)}` : "");
    }
  }
  if (t.startsWith("node.") && (m = raw.match(reHex("^node (\\S+) \\(HEX\\):\\s*([\\s\\S]*)$", "")))) {
    return `${m[1]}: ${prose(m[2], title)}`;
  }
  if (t === "approval.requested" && (m = raw.match(/^factory_project_create:\s*([\s\S]*)$/))) {
    return `Asks to create a project${m[1] ? `: ${prose(m[1], title)}` : ""}`;
  }
  if (t.startsWith("project.") && raw) {
    const s = projectSummary(t, raw, title);
    if (s) return s;
  }
  if (t.startsWith("task.unparked.") &&/^(cleared|overridden)$/.test(raw)) {
    return raw === "overridden" ? "Resumed early" : "Resumed";
  }
  if (!raw) {
    switch (true) {
      case t === "openspec.written": return `Updated ${openspecPath(item.entity_id)}`;
      case t === "openspec.deleted": return `Removed ${openspecPath(item.entity_id)}`;
      case t === "artifact.registered": return `Registered artifact ${item.entity_id}`;
      case t === "project.created": return `Project ${item.entity_id} created`;
      case t === "approval.requested": return taskTitle ? `Approval needed for ${quote(taskTitle)}` : "Waiting for an approval";
      case t === "approval.resolved":
        return (item.status === "REJECTED" ? "Approval rejected" : item.status === "APPROVED" ? "Approval granted" : "Approval resolved") + (taskTitle ? ` for ${quote(taskTitle)}` : "");
      case t === "task.trigger.ready": return taskTitle ? `${quote(taskTitle)} is ready to start` : "Ready to start";
      case !!taskTitle: return taskTitle!;
    }
    return cap(t.split(".").slice(-1)[0].replace(/_/g, " "));
  }
  // "developer done: …", "ceo asked: …": the row already names the agent and what happened.
  const text = prose(raw, title).replace(/^[\w-]+ (?:done|blocked|asked):\s*/, "").replace(/^[\w-]+ (waiting until )/, "$1");
  // A message route ("ceo → owner (notification): …") starts with an agent's name, which stays as it is.
  return /^[\w-]+ → /.test(text) ? text : cap(text);
}
