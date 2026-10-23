"use client";

import { Check, Plus } from "lucide-react";
import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { Action, Overview } from "@/lib/api/types";
import type { BoardTask } from "@/lib/api/types-tasks";
import { cn } from "@/lib/utils";
import { Field, touchInput } from "./field";
import { isConversation, isTerminal } from "./model";
import { useOwnerAction } from "./use-owner-action";

const NONE = "__none";

// NewTask: the "New task" button and its sheet (Action Kind "create"), validated like the terminal form.
export function NewTask({ overview, defaultProject }: { overview?: Overview; defaultProject?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button className="h-10" disabled={!overview?.status.length}><Plus />New task</Button>
      </SheetTrigger>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        {overview && open && <NewTaskForm overview={overview} defaultProject={defaultProject} onDone={() => setOpen(false)} />}
      </SheetContent>
    </Sheet>
  );
}

function NewTaskForm({ overview, defaultProject, onDone }: { overview: Overview; defaultProject?: string; onDone: () => void }) {
  const router = useRouter();
  const projects = overview.status.map((s) => s.project);
  const [project, setProject] = useState(defaultProject && projects.some((p) => p.id === defaultProject) ? defaultProject : projects[0]?.id ?? "");
  const [title, setTitle] = useState("");
  const [text, setText] = useState("");
  const [assignee, setAssignee] = useState(NONE);
  const [priority, setPriority] = useState("5");
  const [deps, setDeps] = useState<string[]>([]);
  const [ready, setReady] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending } = useOwnerAction();

  const candidates = useMemo(() => (overview.tasks as BoardTask[])
    .filter((t) => t.project_id === project && !isTerminal(t.status) && !isConversation(t)), [overview.tasks, project]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    const p = Number(priority.trim());
    if (!project) errs.project = "Choose a project";
    if (!title.trim()) errs.title = "A title is required";
    else if (new TextEncoder().encode(title.trim()).length > 1024) errs.title = "At most 1024 bytes";
    if (new TextEncoder().encode(text).length > 64 * 1024) errs.text = "At most 64 KiB";
    if (!Number.isInteger(p) || priority.trim() === "") errs.priority = "A whole number";
    else if (p < -1000 || p > 1000) errs.priority = "Between -1000 and 1000";
    if (ready && assignee === NONE) errs.assignee = "A ready task needs an assignee";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    const a: Action = { Kind: "create", Project: project, Title: title.trim(), Text: text.trim(), Priority: p,
      Assignee: assignee === NONE ? "" : assignee, Deps: deps.filter((d) => candidates.some((c) => c.id === d)), Ready: ready };
    const res = await run(a, { title: "Create this task?", confirm: "Create task" });
    if (res === null) return;
    onDone();
    const id = /^created (\S+):/.exec(res)?.[1];
    if (id) router.push(`/tasks/${encodeURIComponent(id)}`);
  };

  return (
    <form onSubmit={submit} className="flex h-full min-h-0 flex-col" noValidate>
      <SheetHeader className="border-b px-5 py-4">
        <SheetTitle className="text-lg">New task</SheetTitle>
        <SheetDescription>It lands in the backlog unless you release it as ready.</SheetDescription>
      </SheetHeader>
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-5">
        <Field id="nt-project" label="Project" error={errors.project}>
          <Select value={project} onValueChange={(v) => { setProject(v); setDeps([]); }}>
            <SelectTrigger id="nt-project" className="w-full data-[size=default]:h-11 md:data-[size=default]:h-9"><SelectValue placeholder="Choose a project" /></SelectTrigger>
            <SelectContent position="popper">{projects.map((p) => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}</SelectContent>
          </Select>
        </Field>
        <Field id="nt-title" label="Title" error={errors.title}>
          <Input id="nt-title" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="What needs doing" autoFocus
            aria-invalid={!!errors.title} className={touchInput} />
        </Field>
        <Field id="nt-text" label="Description" hint="Details and acceptance criteria." error={errors.text}>
          <Textarea id="nt-text" value={text} onChange={(e) => setText(e.target.value)} rows={6} className="min-h-32 text-base md:text-sm"
            placeholder={"Implement the export screen.\n\nAcceptance:\n- CSV and JSON\n- works offline"} />
        </Field>
        <div className="grid grid-cols-[1fr_7rem] gap-3">
          <Field id="nt-assignee" label="Assignee" error={errors.assignee}>
            <Select value={assignee} onValueChange={setAssignee}>
              <SelectTrigger id="nt-assignee" className="w-full data-[size=default]:h-11 md:data-[size=default]:h-9"><SelectValue /></SelectTrigger>
              <SelectContent position="popper">
                <SelectItem value={NONE}>Unassigned</SelectItem>
                {overview.agents.map((a) => <SelectItem key={a.name} value={a.name}>{a.name}{a.role ? <span className="text-muted-foreground"> · {a.role}</span> : null}</SelectItem>)}
              </SelectContent>
            </Select>
          </Field>
          <Field id="nt-priority" label="Priority" error={errors.priority}>
            <Input id="nt-priority" inputMode="numeric" value={priority} onChange={(e) => setPriority(e.target.value)} aria-invalid={!!errors.priority} className={touchInput} />
          </Field>
        </div>
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Depends on</legend>
          {candidates.length === 0 ? <p className="text-xs text-muted-foreground">No open tasks in this project.</p> : (
            <div className="flex max-h-48 flex-wrap gap-2 overflow-y-auto">
              {candidates.map((t) => {
                const on = deps.includes(t.id);
                return (
                  <button key={t.id} type="button" aria-pressed={on} onClick={() => setDeps(on ? deps.filter((d) => d !== t.id) : [...deps, t.id])}
                    className={cn("inline-flex h-10 max-w-full items-center gap-1.5 rounded-full border px-3 text-sm transition-colors md:h-8",
                      on ? "border-foreground bg-foreground text-background" : "bg-background hover:bg-accent")}>
                    {on && <Check className="size-3.5" />}
                    <span className="font-mono text-xs opacity-70">{t.id}</span>
                    <span className="truncate">{t.title}</span>
                  </button>
                );
              })}
            </div>
          )}
        </fieldset>
        <label htmlFor="nt-ready" className="flex cursor-pointer items-start justify-between gap-4 rounded-xl border p-4">
          <span>
            <span className="block text-sm font-medium">Release as ready</span>
            <span className="block text-xs text-muted-foreground">The assignee picks it up once its dependencies are done.</span>
          </span>
          <Switch id="nt-ready" checked={ready} onCheckedChange={setReady} />
        </label>
      </div>
      <SheetFooter className="flex-row justify-end gap-2 border-t px-5 py-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
        <Button type="button" variant="outline" className="h-10" onClick={onDone}>Cancel</Button>
        <Button type="submit" className="h-10" disabled={pending}>Create task</Button>
      </SheetFooter>
    </form>
  );
}
