import { TaskPanelHost } from "@/components/app/tasks/panel";

// Tasks: the board (or a task's full page) and, in the @modal slot, a task opened from the board over it
// (task-board-ux 2.1).
export default function TasksLayout({ children, modal }: { children: React.ReactNode; modal: React.ReactNode }) {
  return <>{children}<TaskPanelHost panel={modal} /></>;
}
