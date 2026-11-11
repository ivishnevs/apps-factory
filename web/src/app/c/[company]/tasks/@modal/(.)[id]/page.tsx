"use client";

import { useParams } from "next/navigation";
import { TaskView } from "@/components/app/tasks/view";

// A task opened from the board: the same view as the full page, inside the panel over the board (TaskPanelHost in
// the tasks layout holds the dialog).
export default function TaskPanelPage() {
  const { id } = useParams<{ id: string }>();
  return <TaskView taskId={decodeURIComponent(id)} />;
}
