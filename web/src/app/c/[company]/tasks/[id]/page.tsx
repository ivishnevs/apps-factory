"use client";

import { ArrowLeft } from "lucide-react";
import Link from "@/components/app/link";
import { useParams } from "next/navigation";
import { TaskView } from "@/components/app/tasks/view";

// The task's full page: a reload or a shared link to /tasks/{id}. From the board the same address opens the task in
// a panel over it (@modal/(.)[id], task-board-ux 2.1).
export default function Page() {
  const { id } = useParams<{ id: string }>();
  return (
    <div className="pb-20 md:pb-0">
      <Link href="/tasks" className="-ml-2 mb-3 inline-flex h-10 items-center gap-1.5 rounded-md px-2 text-sm text-muted-foreground transition-colors hover:text-foreground">
        <ArrowLeft className="size-4" />Tasks
      </Link>
      <TaskView taskId={decodeURIComponent(id)} />
    </div>
  );
}
