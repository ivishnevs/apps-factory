import type { FactoryNode, Project, RepoMode } from "@/lib/api/types";
import { cleanText } from "@/lib/humanize";

// A project created without a ready repository is set up on the node that runs its first task
// (openspec/changes/project-creation design D2). setupView says where that stands, in product language.

export const repoModeWords: Record<RepoMode, string> = {
  local: "New local repository",
  github: "New private GitHub repository",
  existing: "Existing repository",
};

export interface SetupView {
  state: "pending" | "running" | "failed";
  title: string; // "Repository is created when the first task runs"
  detail?: string; // the failure reason, or what happens next
  hint?: string; // what the owner can do
  taskId?: string; // the task whose run sets it up (or retries it)
  tone: "info" | "warn" | "bad";
}

export const nodeLabel = (id: string | undefined, nodes?: FactoryNode[]) =>
  id ? (nodes?.find((n) => n.node_id === id)?.name || id) : "";

// setupView: undefined when the repository is ready or the project needs no setup.
export function setupView(p: Project, nodes?: FactoryNode[]): SetupView | undefined {
  const s = p.setup;
  if (!s || s.state === "ready") return undefined;
  const node = nodeLabel(s.node_id, nodes);
  const what = p.repo_mode === "existing" ? "Repository is onboarded" : "Repository is created";
  if (s.state === "running") {
    return { state: "running", tone: "info", title: node ? `Setting up on ${node}` : "Setting up the repository", taskId: s.task_id,
      detail: p.repo_mode === "existing" ? "Its OpenSpec tree is imported before the first task's run starts." : "The first task's run starts once it is ready." };
  }
  if (s.state === "failed") {
    return { state: "failed", tone: "bad", title: node ? `Setup failed on ${node}` : "Repository setup failed", taskId: s.task_id,
      detail: cleanText(s.reason) || "The node could not set up the repository.",
      hint: "Fix the cause on the node, then retry the task. It also retries on its own." };
  }
  const audit = s.audit && s.audit_task_id;
  return { state: "pending", tone: "warn", taskId: s.audit_task_id,
    title: audit ? `${what} when the audit runs` : `${what} when the first task runs`,
    detail: audit ? "The audit task is the project's first task; its run sets the repository up." : "Add a task: its run sets the repository up on a node." };
}
