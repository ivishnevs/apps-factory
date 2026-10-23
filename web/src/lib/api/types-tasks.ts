import type { Action, Task } from "./types";

// Task fields the board and detail use that types.ts does not list yet (internal/tasks/types.go):
// kind ("conversation" for a chat thread kept as a task, "" for work) and paused_by (who paused it).
export type BoardTask = Task & { kind?: string; paused_by?: string };

// describe is an owner action in words, for confirmations and toasts (mirrors internal/tui Action.Describe).
export function describe(a: Action): string {
  switch (a.Kind) {
    case "comment": return `Comment on ${a.TaskID}: ${a.Text}`;
    case "direct": return `Directive to ${a.TaskID}${a.Urgent ? " (urgent)" : ""}: ${a.Text}`;
    case "reassign": return `Reassign ${a.TaskID} to ${a.Assignee}`;
    case "reprioritize": return `Reprioritize ${a.TaskID} to ${a.Priority}`;
    case "cancel": return a.Cascade ? `Cancel ${a.TaskID} and its open children` : `Cancel ${a.TaskID}`;
    case "create": return `Create task “${a.Title}” in ${a.Project}${a.Assignee ? ` for ${a.Assignee}` : ""} (priority ${a.Priority ?? 0}${a.Ready ? ", ready" : ""})`;
    case "pause": return `Pause ${a.TaskID}: no new runs start until you resume it`;
    case "resume": return `Resume ${a.TaskID}`;
    case "retry": return `Retry ${a.TaskID}: release it to run again`;
    case "run": return `Run ${a.TaskID}: release it to the board now`;
  }
  return `${a.Kind} ${a.TaskID ?? ""}`.trim();
}
