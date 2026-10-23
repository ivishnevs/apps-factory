"use client";

import { ArrowLeft, FileSearch } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { EmptyState, ErrorState } from "@/components/app/page";
import { Attempts, Children, Comments, Description, DetailsList, Header, Parks, ReviewNotes, Submission, Timeline, Why } from "@/components/app/tasks/detail";
import { isTerminal, useClock, useHydrated, useOverviewData } from "@/components/app/tasks/model";
import { TaskActions } from "@/components/app/tasks/task-actions";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError, useTask } from "@/lib/api/client";
import type { BoardTask } from "@/lib/api/types-tasks";

export default function Page() {
  const { id } = useParams<{ id: string }>();
  const taskId = decodeURIComponent(id);
  const q = useTask(taskId);
  const hydrated = useHydrated();
  const { data: ov } = useOverviewData();
  const now = useClock();
  const d = hydrated ? q.data : undefined;

  const back = (
    <Link href="/tasks" className="-ml-2 mb-3 inline-flex h-10 items-center gap-1.5 rounded-md px-2 text-sm text-muted-foreground transition-colors hover:text-foreground">
      <ArrowLeft className="size-4" />Tasks
    </Link>
  );
  if (!d) {
    const err = hydrated ? q.error : null;
    return (
      <>
        {back}
        {err ? (err instanceof ApiError && (err.status === 404 || /not (in|found)/.test(err.message))
          ? <EmptyState icon={FileSearch} title={`Task ${taskId} was not found`} hint="It may belong to another company, or the link is mistyped." />
          : <ErrorState error={err} />) : <DetailSkeleton />}
      </>
    );
  }

  const task = d.task as BoardTask;
  const children = d.children ?? [];
  const project = ov?.status.find((s) => s.project.id === task.project_id)?.project.name ?? task.project_id;
  const pr = [...(d.exits ?? [])].reverse().find((e) => e.facts?.pr?.number)?.facts?.pr;
  const attempts = d.attempts ?? [];
  const failed = attempts.length > 0 && !!attempts[attempts.length - 1].outcome && attempts[attempts.length - 1].outcome !== "succeeded";
  const parent = task.parent_task_id ?? task.origin_task;

  return (
    <div className="pb-20 md:pb-0">
      {back}
      <Header task={task} project={project} pr={pr} now={now}
        actions={<TaskActions task={task} agents={ov?.agents ?? []} hasOpenChildren={children.some((c) => !isTerminal(c.status))} failed={failed} />} />
      <div className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem] lg:items-start">
        <div className="grid min-w-0 gap-6 lg:col-start-1 lg:row-start-1">
          {!isTerminal(task.status) && <Why why={d.why} queued={d.queued} task={task} />}
          {isTerminal(task.status) && task.result && <Why why={d.why} task={task} />}
          <Description text={task.description} />
          {task.submission && <Submission sub={task.submission} />}
          <ReviewNotes task={task} />
          <Comments taskId={task.id} comments={d.comments ?? []} now={now} />
        </div>
        <aside className="grid min-w-0 gap-6 lg:col-start-2 lg:row-span-2 lg:row-start-1">
          <DetailsList task={task} parent={parent} cost={d.costs?.total?.cost_usd ?? 0} now={now} />
          <Attempts runs={attempts} now={now} />
          {children.length > 0 && <Children tasks={children} />}
          {(d.parks ?? []).length > 0 && <Parks parks={d.parks ?? []} now={now} />}
        </aside>
        <div className="min-w-0 lg:col-start-1 lg:row-start-2"><Timeline events={d.events ?? []} now={now} /></div>
      </div>
    </div>
  );
}

function DetailSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading task" className="space-y-4">
      <Skeleton className="h-5 w-40" />
      <Skeleton className="h-8 w-3/4" />
      <Skeleton className="h-5 w-1/2" />
      <div className="grid gap-6 pt-4 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="space-y-4"><Skeleton className="h-28 rounded-2xl" /><Skeleton className="h-48 rounded-2xl" /></div>
        <Skeleton className="h-64 rounded-2xl" />
      </div>
    </div>
  );
}
