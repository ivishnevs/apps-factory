"use client";

import { AlertTriangle, Check, FolderInput, GitBranch, Loader2, Lock, Plus } from "lucide-react";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { Field, touchInput } from "@/components/app/tasks/field";
import { useAction } from "@/lib/api/client";
import type { Action, Overview, ProjectCreate, RepoMode } from "@/lib/api/types";
import { cleanText } from "@/lib/humanize";
import { cn } from "@/lib/utils";

// The rules mirror internal/tui ValidateNewProject (and Core's project ID rule); Core has the final word.
const idRule = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$/;
const gitUrl = /^(https?:\/\/|ssh:\/\/|git:\/\/|file:\/\/|[\w.-]+@[\w.-]+:)\S+$/;
const branchRule = /^[A-Za-z0-9._/-]+$/;
const bytes = (s: string) => new TextEncoder().encode(s).length;

// slug turns a name into a project ID: lower case, words joined by dashes (internal/tui Slug).
export function slug(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 64).replace(/-+$/, "");
}

// A request id per submit: Core applies a retried request once.
function requestId() {
  try { return `web-${crypto.randomUUID()}`; } catch { return `web-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`; }
}

const modes: { key: RepoMode; title: string; hint: string; icon: React.ElementType }[] = [
  { key: "local", title: "New local repository", hint: "A fresh git repository on the node, branch main.", icon: GitBranch },
  { key: "github", title: "New private GitHub repository", hint: "Created under your account and cloned; work lands as pull requests.", icon: Lock },
  { key: "existing", title: "Existing project", hint: "A folder on the node or a repository URL the node clones.", icon: FolderInput },
];

type Errors = Partial<Record<"id" | "name" | "description" | "workspace" | "repo_url" | "default_branch", string>>;

export function validateProject(p: ProjectCreate, taken: string[]): Errors {
  const e: Errors = {};
  if (!p.id) e.id = "An ID is required";
  else if (!idRule.test(p.id)) e.id = "Letters, digits, - and _; start with a letter or digit";
  else if (taken.includes(p.id)) e.id = `A project ${p.id} already exists`;
  if (!p.name.trim()) e.name = "A name is required";
  else if (bytes(p.name) > 200) e.name = "At most 200 bytes";
  if (bytes(p.description) > 64 * 1024) e.description = "At most 64 KiB";
  if (p.repo_mode === "existing") {
    if (!p.workspace && !p.repo_url) e.workspace = "Give a folder on the node, a repository URL, or both";
    else if (p.workspace && !p.workspace.startsWith("/")) e.workspace = "An absolute path on the node, starting with /";
    if (p.repo_url && !gitUrl.test(p.repo_url)) e.repo_url = "An https, ssh or git@host:owner/repo URL";
    const b = p.default_branch ?? "";
    if (b && (!branchRule.test(b) || b.includes("..") || b.endsWith("/") || b.endsWith(".lock"))) e.default_branch = "Not a valid branch name";
  }
  return e;
}

// NewProject: a "New project" trigger and its sheet (Action Kind "create_project"). Core's refusal stays under
// the form; on success the owner lands on the board filtered by the new project.
export function NewProject({ overview, variant = "outline", size = "default", label = "New project", className }: {
  overview?: Overview; variant?: "default" | "outline" | "ghost"; size?: "default" | "sm"; label?: string; className?: string;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button variant={variant} size={size} className={cn(size === "default" && "h-10", className)} disabled={!overview}><Plus />{label}</Button>
      </SheetTrigger>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        {overview && open && <NewProjectForm overview={overview} onDone={() => setOpen(false)} />}
      </SheetContent>
    </Sheet>
  );
}

function NewProjectForm({ overview, onDone }: { overview: Overview; onDone: () => void }) {
  const router = useRouter();
  const action = useAction();
  const [name, setName] = useState("");
  const [id, setId] = useState("");
  const [idEdited, setIdEdited] = useState(false);
  const [description, setDescription] = useState("");
  const [mode, setMode] = useState<RepoMode>("local");
  const [workspace, setWorkspace] = useState("");
  const [repoUrl, setRepoUrl] = useState("");
  const [branch, setBranch] = useState("");
  const [audit, setAudit] = useState(true);
  const [openspec, setOpenspec] = useState(true);
  const [errors, setErrors] = useState<Errors>({});
  const [refusal, setRefusal] = useState("");
  const refusalRef = useRef<HTMLDivElement>(null);
  const taken = overview.status.map((s) => s.project.id);

  useEffect(() => { if (refusal) refusalRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }); }, [refusal]);

  const onName = (v: string) => { setName(v); if (!idEdited) setId(slug(v)); };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (action.isPending) return;
    const p: ProjectCreate = { id: id.trim(), name: name.trim(), description: description.trim(), request_id: requestId(), openspec: { enabled: openspec }, repo_mode: mode };
    if (mode === "existing") Object.assign(p, { workspace: workspace.trim() || undefined, repo_url: repoUrl.trim() || undefined, default_branch: branch.trim() || undefined, audit });
    const errs = validateProject(p, taken);
    setErrors(errs);
    if (Object.keys(errs).length) return;
    setRefusal("");
    const a: Action = { Kind: "create_project", NewProject: p };
    try {
      await action.mutateAsync(a);
      toast.success(`Project ${p.name} created`, {
        description: mode === "existing" ? (audit ? "Core is onboarding it; the audit task follows." : "Core is onboarding it.") : undefined,
      });
      onDone();
      router.push(`/tasks?project=${encodeURIComponent(p.id)}`);
    } catch (err) {
      setRefusal(cleanText(err instanceof Error ? err.message : String(err)) || "Core refused the project");
    }
  };

  const err = (k: keyof Errors) => errors[k];
  const invalid = (k: keyof Errors) => ({ "aria-invalid": !!errors[k], "aria-describedby": errors[k] ? `np-${k}-error` : undefined });

  return (
    <form onSubmit={submit} className="flex h-full min-h-0 flex-col" noValidate>
      <SheetHeader className="border-b px-5 py-4">
        <SheetTitle className="text-lg">New project</SheetTitle>
        <SheetDescription>Core sets up its repository; then you or your team add tasks.</SheetDescription>
      </SheetHeader>
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-5">
        <Field id="np-name" label="Name" error={err("name")}>
          <Input id="np-name" value={name} onChange={(e) => onName(e.target.value)} placeholder="Advent gift" autoFocus autoComplete="off"
            {...invalid("name")} className={touchInput} />
        </Field>
        <Field id="np-id" label="ID" error={err("id")} hint="Used in task IDs and URLs; it can't be changed later.">
          <Input id="np-id" value={id} onChange={(e) => { setId(e.target.value); setIdEdited(e.target.value !== ""); }} placeholder="advent-gift"
            autoComplete="off" autoCapitalize="none" spellCheck={false} {...invalid("id")} className={cn(touchInput, "font-mono")} />
        </Field>
        <Field id="np-description" label="Description" error={err("description")} hint="What it is and for whom; agents read it.">
          <Textarea id="np-description" value={description} onChange={(e) => setDescription(e.target.value)} rows={3}
            className="min-h-20 text-base md:text-sm" placeholder="A daily gift calendar app for families." />
        </Field>

        <fieldset className="space-y-2">
          <legend className="mb-1.5 text-sm font-medium">Repository</legend>
          <div role="radiogroup" aria-label="Repository" className="grid gap-2">
            {modes.map((m) => {
              const on = mode === m.key;
              return (
                <button key={m.key} type="button" role="radio" aria-checked={on} onClick={() => setMode(m.key)}
                  className={cn("flex min-h-14 w-full items-start gap-3 rounded-xl border p-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
                    on ? "border-foreground/80 bg-accent/60 dark:border-foreground/60" : "hover:bg-accent/40")}>
                  <span className={cn("flex size-8 shrink-0 items-center justify-center rounded-lg", on ? "bg-foreground text-background" : "bg-muted text-muted-foreground")}>
                    <m.icon className="size-4" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex flex-wrap items-center gap-x-2 text-sm font-medium">{m.title}
                      {m.key === "local" && <span className="rounded-full bg-muted px-1.5 text-[11px] font-normal leading-5 text-muted-foreground">Default</span>}</span>
                    <span className="block text-xs text-muted-foreground">{m.hint}</span>
                  </span>
                  <span aria-hidden className={cn("mt-1 flex size-4 shrink-0 items-center justify-center rounded-full border", on ? "border-foreground bg-foreground text-background" : "border-muted-foreground/40")}>
                    {on && <Check className="size-3" />}
                  </span>
                </button>
              );
            })}
          </div>
        </fieldset>

        {mode === "existing" && (
          <div className="space-y-4 rounded-xl border bg-muted/30 p-4">
            <Field id="np-workspace" label="Folder on the node" error={err("workspace")} hint="An absolute path; leave empty to clone the URL.">
              <Input id="np-workspace" value={workspace} onChange={(e) => setWorkspace(e.target.value)} placeholder="/Users/me/projects/app"
                autoComplete="off" autoCapitalize="none" spellCheck={false} {...invalid("workspace")} className={cn(touchInput, "bg-background font-mono")} />
            </Field>
            <Field id="np-repo_url" label="Repository URL" error={err("repo_url")} hint="Optional with a folder; pull requests go here.">
              <Input id="np-repo_url" value={repoUrl} onChange={(e) => setRepoUrl(e.target.value)} placeholder="https://github.com/me/app" inputMode="url"
                autoComplete="off" autoCapitalize="none" spellCheck={false} {...invalid("repo_url")} className={cn(touchInput, "bg-background font-mono")} />
            </Field>
            <Field id="np-default_branch" label="Branch" error={err("default_branch")} hint="Empty: the repository's default branch.">
              <Input id="np-default_branch" value={branch} onChange={(e) => setBranch(e.target.value)} placeholder="main"
                autoComplete="off" autoCapitalize="none" spellCheck={false} {...invalid("default_branch")} className={cn(touchInput, "bg-background font-mono")} />
            </Field>
            <label htmlFor="np-audit" className="flex cursor-pointer items-start justify-between gap-4 rounded-lg border bg-background p-3">
              <span>
                <span className="block text-sm font-medium">Run an audit first</span>
                <span className="block text-xs text-muted-foreground">A read-only audit task for the product manager: code, build, product state and options.</span>
              </span>
              <Switch id="np-audit" checked={audit} onCheckedChange={setAudit} />
            </label>
          </div>
        )}

        <label htmlFor="np-openspec" className="flex cursor-pointer items-start justify-between gap-4 rounded-xl border p-4">
          <span>
            <span className="block text-sm font-medium">OpenSpec</span>
            <span className="block text-xs text-muted-foreground">Specs and changes are kept in Core and given to every run{mode === "existing" ? "; an existing openspec/ is imported" : ""}.</span>
          </span>
          <Switch id="np-openspec" checked={openspec} onCheckedChange={setOpenspec} />
        </label>

        {refusal && (
          <div ref={refusalRef} role="alert" className="flex items-start gap-3 rounded-xl border border-red-500/30 bg-red-500/5 p-4 text-sm">
            <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0 text-red-500" />
            <div className="min-w-0">
              <p className="font-medium text-red-700 dark:text-red-300">Core refused the project</p>
              <p className="mt-0.5 break-words text-foreground/80">{refusal}</p>
            </div>
          </div>
        )}
      </div>
      <SheetFooter className="flex-row justify-end gap-2 border-t px-5 py-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
        <Button type="button" variant="outline" className="h-10" onClick={onDone}>Cancel</Button>
        <Button type="submit" className="h-10" disabled={action.isPending}>
          {action.isPending ? <><Loader2 className="animate-spin" />Creating…</> : "Create project"}
        </Button>
      </SheetFooter>
    </form>
  );
}
