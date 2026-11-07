import { columnOf, isConversation, needsOwner, runsByTask, type ColumnKey } from "@/components/app/tasks/model";
import type { Overview, Project } from "@/lib/api/types";
import type { BoardTask } from "@/lib/api/types-tasks";

// ProjectSummary (projects-page 1.1): a project's state from the overview — tasks by board column, what needs you,
// the last activity and the cost of its ended runs.
export interface ProjectSummary {
  project: Project; counts: Record<ColumnKey, number>; open: number; needsYou: number; lastActivity: number; cost: number;
  live: number;
}

export function summarize(ov: Overview): ProjectSummary[] {
  const runs = runsByTask(ov);
  const costByTask = new Map((ov.task_costs?.groups ?? []).map((g) => [g.key, g.cost_usd]));
  return ov.status.map(({ project }) => {
    const tasks = (ov.tasks as BoardTask[]).filter((t) => t.project_id === project.id && !isConversation(t));
    const counts: Record<ColumnKey, number> = { backlog: 0, waiting: 0, ready: 0, running: 0, review: 0, done: 0 };
    let needsYou = 0, lastActivity = project.updated_at, cost = 0, live = 0;
    for (const t of tasks) {
      const c = columnOf(t, runs);
      counts[c]++;
      if (c !== "done" && needsOwner(t)) needsYou++;
      if (runs.live.has(t.id)) live++;
      lastActivity = Math.max(lastActivity, t.updated_at);
      cost += costByTask.get(t.id) ?? 0;
    }
    return { project, counts, open: tasks.length - counts.done, needsYou, lastActivity, cost, live };
  });
}
