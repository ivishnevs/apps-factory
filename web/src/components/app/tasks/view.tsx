"use client";

import { FileSearch } from "lucide-react";
import { useMemo } from "react";
import { TaskMaterials } from "@/components/app/materials/task-materials";
import { EmptyState, ErrorState } from "@/components/app/page";
import { Attempts, Children, Comments, Description, DetailsList, Header, Parks, PossibleDuplicates, ReviewNotes, Submission, Timeline, Why } from "@/components/app/tasks/detail";
import { columnOf, isTerminal, runsByTask, useClock, useHydrated, useOverviewData } from "@/components/app/tasks/model";
import { TaskActions } from "@/components/app/tasks/task-actions";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError, useTask } from "@/lib/api/client";
import type { BoardTask } from "@/lib/api/types-tasks";
import { lookupFrom } from "@/lib/humanize";

// TaskView is a task as the full page and the panel over the board show it. Until its details arrive it shows
// what the board already knows of the task (header and description from the overview, task-board-ux 2.2) and
// placeholders for the rest; without that (a direct link before the overview), a skeleton.
export function TaskView({ taskId }: { taskId: string }) {
  const q = useTask(taskId);
  const hydrated = useHydrated();
  const { data: ov } = useOverviewData();
  const now = useClock();
  const d = hydrated ? q.data : undefined;
  const look = useMemo(() => lookupFrom([...(ov?.tasks ?? []), ...(q.data?.children ?? [])]), [ov?.tasks, q.data?.children]);
  const runs = useMemo(() => runsByTask(ov), [ov]);
  const known = (d?.task ?? ov?.tasks.find((t) => t.id === taskId)) as BoardTask | undefined;
  const project = known ? ov?.status.find((s) => s.project.id === known.project_id)?.project.name ?? known.project_id : "";

  if (!d) {
    const err = hydrated ? q.error : null;
    if (err) {
      return err instanceof ApiError && (err.status === 404 || /not (in|found)/.test(err.message))
        ? <EmptyState icon={FileSearch} title="Task not found" hint="It may belong to another company, or the link is mistyped." />
        : <ErrorState error={err} />;
    }
    if (!known) return <DetailSkeleton />;
    return (
      <div aria-busy="true">
        <Header task={known} project={project} now={now} column={columnOf(known, runs)}
          actions={<Skeleton aria-hidden className="h-9 w-28 shrink-0 rounded-md" />} />
        <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_20rem] lg:items-start">
          <div className="grid min-w-0 grid-cols-1 gap-6">
            <Skeleton className="h-28 rounded-2xl" />
            <Description text={known.description} />
            <span className="sr-only">Loading the task’s details…</span>
          </div>
          <Skeleton className="h-64 rounded-2xl" />
        </div>
      </div>
    );
  }

  const task = d.task as BoardTask;
  const children = d.children ?? [];
  const pr = [...(d.exits ?? [])].reverse().find((e) => e.facts?.pr?.number)?.facts?.pr;
  const attempts = d.attempts ?? [];
  const failed = attempts.length > 0 && !!attempts[attempts.length - 1].outcome && attempts[attempts.length - 1].outcome !== "succeeded";
  const parent = task.parent_task_id ?? task.origin_task;

  return (
    <>
      <Header task={task} project={project} pr={pr} now={now} column={columnOf(task, runs)}
        actions={<TaskActions task={task} agents={ov?.agents ?? []} hasOpenChildren={children.some((c) => !isTerminal(c.status))} failed={failed} />} />
      <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_20rem] lg:items-start">
        <div className="grid min-w-0 grid-cols-1 gap-6 lg:col-start-1 lg:row-start-1">
          {!isTerminal(task.status) && <Why why={d.why} queued={d.queued} task={task} look={look} now={now} />}
          {isTerminal(task.status) && task.result && <Why why={d.why} task={task} look={look} now={now} />}
          {task.status === "DONE" && <TaskMaterials taskId={task.id} featured now={now} />}
          <Description text={task.description} />
          {task.submission && <Submission sub={task.submission} look={look} />}
          {task.status !== "DONE" && <TaskMaterials taskId={task.id} featured={false} now={now} />}
          <PossibleDuplicates task={task} look={look} />
          <ReviewNotes task={task} look={look} />
          <Comments taskId={task.id} comments={d.comments ?? []} now={now} look={look} closed={isTerminal(task.status)} />
        </div>
        <aside className="grid min-w-0 grid-cols-1 gap-6 lg:col-start-2 lg:row-span-2 lg:row-start-1">
          <DetailsList task={task} parent={parent} cost={d.costs?.total?.cost_usd ?? 0} now={now} look={look} />
          <Attempts runs={attempts} now={now} />
          {children.length > 0 && <Children tasks={children} />}
          {(d.parks ?? []).length > 0 && <Parks parks={d.parks ?? []} now={now} look={look} />}
        </aside>
        <div className="min-w-0 lg:col-start-1 lg:row-start-2"><Timeline events={d.events ?? []} now={now} look={look} /></div>
      </div>
    </>
  );
}

function DetailSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading task" className="space-y-4">
      <Skeleton className="h-5 w-40" />
      <Skeleton className="h-8 w-3/4" />
      <Skeleton className="h-5 w-1/2" />
      <div className="grid grid-cols-1 gap-6 pt-4 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="space-y-4"><Skeleton className="h-28 rounded-2xl" /><Skeleton className="h-48 rounded-2xl" /></div>
        <Skeleton className="h-64 rounded-2xl" />
      </div>
    </div>
  );
}
