"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowUpCircle, BookOpen, Download, FileText, FolderGit2, GitBranch, Loader2, Plus, RefreshCw, Search, Sparkles, Trash2, Upload } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Markdown } from "@/components/app/run/markdown";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ApiError, core, useCatalog, useCompanyInfo, useTeams } from "@/lib/api/client";
import { canManage } from "@/lib/api/types-account";
import { scopeLabel, type InstalledSkill, type LibrarySkill, type SkillFile, type SkillSource } from "@/lib/api/types-skills";
import { currentCompany, csrf } from "@/lib/company";
import { ago } from "@/lib/format";
import { cn } from "@/lib/utils";

// Skills (skill-registry 6.1, design D3–D7, D10): the company's skill sources (Factory's built-in skills, git
// repositories fetched by Core or, when private, by a node of the company, uploads), the library indexed from them
// with each SKILL.md and its files, and the installed skills with their scope and updates. Installing writes the
// catalog, so history and rollback cover skills. Owners and admins change them; every role reads.

const statusWords: Record<string, { label: string; tone: string }> = {
  ok: { label: "Synced", tone: "text-emerald-700 dark:text-emerald-400" },
  syncing: { label: "Syncing", tone: "text-sky-700 dark:text-sky-400" },
  waiting_for_node: { label: "Waiting for a connector", tone: "text-amber-700 dark:text-amber-400" },
  failed: { label: "Failed", tone: "text-red-700 dark:text-red-400" },
};

const fail = (e: unknown) => toast.error(e instanceof Error ? e.message : String(e));
const unavailable = (e: unknown) => e instanceof ApiError && (e.status === 404 || e.status === 501);

function useSkillsQueries() {
  const sources = useQuery({ queryKey: ["skills", "sources"], queryFn: () => core<SkillSource[]>("/skills/sources"), refetchInterval: 10000 });
  const installed = useQuery({ queryKey: ["skills", "installed"], queryFn: () => core<InstalledSkill[]>("/skills/installed") });
  return { sources, installed };
}

// Viewer: a library skill's SKILL.md rendered, and its file tree.
function SkillViewer({ skill, onClose }: { skill: LibrarySkill | null; onClose: () => void }) {
  const base = skill ? `/skills/library/${encodeURIComponent(skill.source)}/${encodeURIComponent(skill.name)}/files` : "";
  const files = useQuery({ queryKey: ["skills", "files", base], queryFn: () => core<SkillFile[]>(base), enabled: !!skill });
  const [path, setPath] = useState("SKILL.md");
  const file = useQuery({
    queryKey: ["skills", "file", base, path], enabled: !!skill,
    queryFn: async () => {
      const res = await fetch(`/api/core${base}/${path.split("/").map(encodeURIComponent).join("/")}`, { credentials: "same-origin", headers: { "X-Factory-Company": currentCompany() } });
      if (!res.ok) throw new ApiError((await res.text()).trim() || `HTTP ${res.status}`, res.status);
      return res.text();
    },
  });
  return (
    <Sheet open={!!skill} onOpenChange={(o) => { if (!o) { onClose(); setPath("SKILL.md"); } }}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-3xl">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle className="font-mono">{skill?.name}</SheetTitle>
          <SheetDescription>{skill?.description}</SheetDescription>
        </SheetHeader>
        <div className="grid min-h-0 flex-1 gap-0 md:grid-cols-[14rem_1fr]">
          <ul className="max-h-40 overflow-y-auto border-b p-2 md:max-h-none md:border-r md:border-b-0">
            {files.isLoading && <li className="p-2"><Loader2 className="size-4 animate-spin" /></li>}
            {(files.data ?? []).map((f) => (
              <li key={f.path}>
                <button onClick={() => setPath(f.path)} className={cn("flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left font-mono text-xs hover:bg-muted", f.path === path && "bg-muted font-medium")}>
                  <FileText className="size-3.5 shrink-0" /><span className="truncate">{f.path}</span>
                </button>
              </li>
            ))}
          </ul>
          <div className="min-h-0 overflow-y-auto p-5">
            {file.error ? <ErrorState error={file.error} /> : file.isLoading ? <LoadingRows rows={3} /> :
              path.endsWith(".md") ? <Markdown text={(file.data ?? "").replace(/^---\n[\s\S]*?\n---\n/, "")} variant="document" /> :
                <pre className="overflow-x-auto rounded-lg border bg-muted/30 p-3 font-mono text-xs">{file.data}</pre>}
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

function InstallSheet({ skill, base, onClose }: { skill: LibrarySkill | null; base: number; onClose: () => void }) {
  const teams = useTeams().data?.teams ?? [];
  const qc = useQueryClient();
  const [scope, setScope] = useState("all");
  const [busy, setBusy] = useState(false);
  const install = async () => {
    if (!skill) return;
    setBusy(true);
    try {
      await core("/skills/install", { method: "POST", body: JSON.stringify({ base, source: skill.source, name: skill.name, scope: scope === "all" ? "" : scope }) });
      toast.success(`${skill.name} installed`);
      await qc.invalidateQueries({ queryKey: ["skills"] });
      await qc.invalidateQueries({ queryKey: ["catalog"] });
      onClose();
    } catch (e) { fail(e); } finally { setBusy(false); }
  };
  return (
    <Sheet open={!!skill} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>Install {skill?.name}</SheetTitle>
          <SheetDescription>It becomes part of the company&apos;s catalog (a new version); agents use it once their form includes it.</SheetDescription>
        </SheetHeader>
        <div className="space-y-4 px-5 py-5">
          <label className="block space-y-1.5 text-sm"><span className="font-medium">Who may use it</span>
            <Select value={scope} onValueChange={setScope}>
              <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent position="popper">
                <SelectItem value="all">Every team</SelectItem>
                {teams.map((t) => <SelectItem key={t.name} value={`team:${t.name}`}>Team {t.name}</SelectItem>)}
              </SelectContent>
            </Select>
          </label>
          <Button className="h-10 w-full" onClick={install} disabled={busy}><Download className="size-4" />Install</Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}

function AddSource({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [mode, setMode] = useState<"git" | "upload">("git");
  const [url, setUrl] = useState("");
  const [ref, setRef] = useState("");
  const [subpath, setSubpath] = useState("");
  const [access, setAccess] = useState<"public" | "node">("public");
  const [update, setUpdate] = useState<"pinned" | "track">("pinned");
  const [name, setName] = useState("");
  const [archive, setArchive] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      if (mode === "git") {
        if (!/^https:\/\//.test(url.trim())) throw new Error("An https:// repository address.");
        await core("/skills/sources", { method: "POST", body: JSON.stringify({ url: url.trim(), ref: ref.trim(), subpath: subpath.trim(), access, update }) });
      } else {
        if (!archive) throw new Error("Choose a .zip or .tar.gz archive.");
        const form = new FormData();
        form.set("name", name.trim() || archive.name.replace(/\.(zip|tar\.gz|tgz)$/i, ""));
        form.set("archive", archive);
        const res = await fetch("/api/core/skills/sources/upload", { method: "POST", body: form, credentials: "same-origin",
          headers: { "X-Factory-Web": "1", "X-Factory-CSRF": csrf(), "X-Factory-Company": currentCompany() } });
        if (!res.ok) throw new ApiError((await res.text()).trim() || `HTTP ${res.status}`, res.status);
      }
      toast.success("Source added; its skills appear in the Library once it is synced");
      await qc.invalidateQueries({ queryKey: ["skills"] });
      onClose();
    } catch (err) { fail(err); } finally { setBusy(false); }
  };
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>Add a skill source</SheetTitle>
          <SheetDescription>A repository of Agent Skills (SKILL.md folders) or an archive. Added once, for every team.</SheetDescription>
        </SheetHeader>
        <form onSubmit={submit} className="space-y-4 overflow-y-auto px-5 py-5">
          <div role="radiogroup" aria-label="Kind" className="grid grid-cols-2 gap-2">
            {([["git", "Repository", GitBranch], ["upload", "Upload", Upload]] as const).map(([k, label, Icon]) => (
              <button key={k} type="button" role="radio" aria-checked={mode === k} onClick={() => setMode(k)}
                className={cn("flex h-11 items-center justify-center gap-2 rounded-lg border text-sm", mode === k ? "border-foreground/80 bg-accent/60" : "hover:bg-accent/40")}>
                <Icon className="size-4" />{label}
              </button>
            ))}
          </div>
          {mode === "git" ? (
            <>
              <label className="block space-y-1.5 text-sm"><span className="font-medium">Repository</span>
                <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://github.com/acme/agent-skills" required spellCheck={false} />
              </label>
              <div className="grid grid-cols-2 gap-3">
                <label className="block space-y-1.5 text-sm"><span className="font-medium">Branch or tag</span><Input value={ref} onChange={(e) => setRef(e.target.value)} placeholder="main" /></label>
                <label className="block space-y-1.5 text-sm"><span className="font-medium">Folder</span><Input value={subpath} onChange={(e) => setSubpath(e.target.value)} placeholder="(the whole repository)" /></label>
              </div>
              <label className="block space-y-1.5 text-sm"><span className="font-medium">Access</span>
                <Select value={access} onValueChange={(v) => setAccess(v as "public" | "node")}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent position="popper">
                    <SelectItem value="public">Public: App Factory fetches it</SelectItem>
                    <SelectItem value="node">Private: one of your connectors fetches it with its git access</SelectItem>
                  </SelectContent>
                </Select>
              </label>
              <label className="block space-y-1.5 text-sm"><span className="font-medium">Updates</span>
                <Select value={update} onValueChange={(v) => setUpdate(v as "pinned" | "track")}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent position="popper">
                    <SelectItem value="pinned">Pinned: I review and install updates</SelectItem>
                    <SelectItem value="track">Track: install new versions on every sync</SelectItem>
                  </SelectContent>
                </Select>
              </label>
            </>
          ) : (
            <>
              <label className="block space-y-1.5 text-sm"><span className="font-medium">Name</span><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="brand-skills" /></label>
              <input ref={fileRef} type="file" accept=".zip,.tar.gz,.tgz" className="hidden" onChange={(e) => setArchive(e.target.files?.[0] ?? null)} />
              <Button type="button" variant="outline" className="h-10 w-full justify-start" onClick={() => fileRef.current?.click()}>
                <Upload className="size-4" /><span className="truncate">{archive ? archive.name : "Choose a .zip or .tar.gz"}</span>
              </Button>
            </>
          )}
          <Button type="submit" className="h-10 w-full" disabled={busy}><Plus className="size-4" />Add source</Button>
        </form>
      </SheetContent>
    </Sheet>
  );
}

function Installed({ manage, base }: { manage: boolean; base: number }) {
  const { installed } = useSkillsQueries();
  const qc = useQueryClient();
  const confirm = useConfirm();
  if (installed.isLoading) return <LoadingRows />;
  if (installed.error) return <ErrorState error={installed.error} />;
  const list = installed.data ?? [];
  if (list.length === 0) return <EmptyState icon={Sparkles} title="No skills installed" hint="Install skills from the Library; agents then pick them in their form." />;
  const act = async (path: string, body: object, done: string) => {
    try { await core(path, { method: "POST", body: JSON.stringify({ base, ...body }) }); toast.success(done); await qc.invalidateQueries({ queryKey: ["skills"] }); await qc.invalidateQueries({ queryKey: ["catalog"] }); }
    catch (e) { fail(e); }
  };
  return (
    <ul className="divide-y overflow-hidden rounded-2xl border bg-card shadow-xs">
      {list.map((s) => (
        <li key={s.name} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center md:px-5">
          <div className="min-w-0 flex-1">
            <p className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-sm font-medium">{s.name}</span>
              <Badge variant="secondary" className="font-normal">{s.source === "factory" ? "Built in" : s.source}</Badge>
              <Badge variant="outline" className="font-normal">{scopeLabel(s.scope)}</Badge>
            </p>
            {s.description && <p className="text-sm text-muted-foreground">{s.description}</p>}
            {s.update && (
              <p className="mt-1 flex items-center gap-1.5 text-xs text-sky-700 dark:text-sky-400">
                <ArrowUpCircle className="size-3.5" />Update available{s.update.files?.length ? `: ${s.update.files.map((f) => `${f.change} ${f.path}`).join(", ")}` : ""}
              </p>
            )}
          </div>
          {manage && (
            <div className="flex gap-2">
              {s.update && <Button className="h-10" onClick={() => act("/skills/install", { source: s.source, name: s.name, scope: s.scope ?? "" }, `${s.name} updated`)}>Update</Button>}
              <Button variant="ghost" className="h-10 text-red-600 hover:text-red-700 dark:text-red-400" aria-label={`Uninstall ${s.name}`}
                onClick={async () => { if (await confirm({ title: `Uninstall ${s.name}?`, description: "Refused while an agent still uses it.", confirm: "Uninstall", destructive: true })) await act("/skills/uninstall", { name: s.name }, `${s.name} uninstalled`); }}>
                <Trash2 className="size-4" />
              </Button>
            </div>
          )}
        </li>
      ))}
    </ul>
  );
}

function Library({ manage, base }: { manage: boolean; base: number }) {
  const [q, setQ] = useState("");
  const [source, setSource] = useState("all");
  const { sources } = useSkillsQueries();
  const library = useQuery({
    queryKey: ["skills", "library", q, source],
    queryFn: () => core<LibrarySkill[]>(`/skills/library?${new URLSearchParams({ q, ...(source === "all" ? {} : { source }) })}`),
  });
  const [viewing, setViewing] = useState<LibrarySkill | null>(null);
  const [installing, setInstalling] = useState<LibrarySkill | null>(null);
  return (
    <>
      <div className="mb-4 flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search by name, description or category" className="h-10 pl-9" aria-label="Search skills" />
        </div>
        <Select value={source} onValueChange={setSource}>
          <SelectTrigger className="w-full sm:w-48" aria-label="Source"><SelectValue /></SelectTrigger>
          <SelectContent position="popper">
            <SelectItem value="all">All sources</SelectItem>
            {(sources.data ?? []).map((s) => <SelectItem key={s.id} value={s.id}>{s.id}</SelectItem>)}
          </SelectContent>
        </Select>
      </div>
      {library.isLoading ? <LoadingRows /> : library.error ? <ErrorState error={library.error} /> : (library.data ?? []).length === 0 ? (
        <EmptyState icon={BookOpen} title={q ? "No skill matches" : "The library is empty"} hint="Add a source; its skills are indexed here." />
      ) : (
        <ul className="grid gap-3 md:grid-cols-2">
          {library.data!.map((s) => (
            <li key={`${s.source}/${s.name}`} className="flex flex-col gap-2 rounded-xl border bg-card p-4">
              <p className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-sm font-medium">{s.name}</span>
                <Badge variant="secondary" className="font-normal">{s.source === "factory" ? "Built in" : s.source}</Badge>
                {s.installed && <Badge className="font-normal">Installed</Badge>}
              </p>
              {s.error ? <p className="flex items-start gap-1.5 text-sm text-red-700 dark:text-red-400"><AlertTriangle className="mt-0.5 size-4 shrink-0" />{s.error}</p>
                : <p className="text-sm text-muted-foreground">{s.description}</p>}
              {s.categories && s.categories.length > 0 && <p className="flex flex-wrap gap-1">{s.categories.map((c) => <Badge key={c} variant="outline" className="font-normal">{c}</Badge>)}</p>}
              {!s.error && (
                <div className="mt-auto flex gap-2 pt-1">
                  <Button variant="outline" className="h-9" onClick={() => setViewing(s)}><BookOpen className="size-4" />Read</Button>
                  {manage && !s.installed && <Button className="h-9" onClick={() => setInstalling(s)}><Download className="size-4" />Install…</Button>}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      <SkillViewer skill={viewing} onClose={() => setViewing(null)} />
      <InstallSheet skill={installing} base={base} onClose={() => setInstalling(null)} />
    </>
  );
}

function Sources({ manage }: { manage: boolean }) {
  const { sources } = useSkillsQueries();
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [adding, setAdding] = useState(false);
  const act = async (path: string, method: "POST" | "DELETE", done: string) => {
    try { await core(path, { method, body: method === "POST" ? "{}" : undefined }); toast.success(done); await qc.invalidateQueries({ queryKey: ["skills"] }); }
    catch (e) { fail(e); }
  };
  return (
    <>
      {manage && <div className="mb-4 flex justify-end"><Button className="h-10" onClick={() => setAdding(true)}><Plus className="size-4" />Add a source</Button></div>}
      {sources.isLoading ? <LoadingRows /> : sources.error ? <ErrorState error={sources.error} /> : (
        <ul className="divide-y overflow-hidden rounded-2xl border bg-card shadow-xs">
          {(sources.data ?? []).map((s) => {
            const st = statusWords[s.status] ?? { label: s.status, tone: "text-muted-foreground" };
            return (
              <li key={s.id} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center md:px-5">
                <div className="min-w-0 flex-1">
                  <p className="flex flex-wrap items-center gap-2">
                    {s.kind === "upload" ? <Upload className="size-4 text-muted-foreground" /> : s.kind === "builtin" ? <Sparkles className="size-4 text-muted-foreground" /> : <FolderGit2 className="size-4 text-muted-foreground" />}
                    <span className="font-medium">{s.kind === "builtin" ? "Factory skills" : s.id}</span>
                    <span className={cn("text-xs", st.tone)}>{st.label}</span>
                    {s.access === "node" && <Badge variant="outline" className="font-normal">private, via a connector</Badge>}
                    {s.update === "track" && <Badge variant="outline" className="font-normal">tracks updates</Badge>}
                  </p>
                  {s.url && <p className="truncate font-mono text-xs text-muted-foreground">{s.url}{s.ref ? `@${s.ref}` : ""}{s.subpath ? ` · ${s.subpath}` : ""}</p>}
                  <p className="text-xs text-muted-foreground">
                    {s.skills ?? 0} skills{s.commit ? ` · ${s.commit.slice(0, 8)}` : ""}{s.synced_at ? ` · synced ${ago(s.synced_at)}` : ""}
                  </p>
                  {s.error && <p className="text-xs text-red-700 dark:text-red-400">{s.error}</p>}
                </div>
                {manage && s.kind !== "builtin" && (
                  <div className="flex gap-2">
                    <Button variant="outline" className="h-10" onClick={() => act(`/skills/sources/${encodeURIComponent(s.id)}/sync`, "POST", "Sync started")}><RefreshCw className="size-4" />Sync</Button>
                    <Button variant="ghost" className="h-10 text-red-600 hover:text-red-700 dark:text-red-400" aria-label={`Remove ${s.id}`}
                      onClick={async () => { if (await confirm({ title: `Remove ${s.id}?`, description: "Refused while one of its skills is installed.", confirm: "Remove", destructive: true })) await act(`/skills/sources/${encodeURIComponent(s.id)}`, "DELETE", "Source removed"); }}>
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <AddSource open={adding} onClose={() => setAdding(false)} />
    </>
  );
}

export default function Page() {
  const manage = canManage(useCompanyInfo().data?.role);
  const base = useCatalog().data?.version ?? 0;
  const { sources } = useSkillsQueries();
  const off = useMemo(() => unavailable(sources.error), [sources.error]);
  return (
    <>
      <PageHeader title="Skills" description="What your agents know how to do: built-in skills and your own, installed once for the whole company." />
      {off ? (
        <EmptyState icon={Sparkles} title="Skills are not available on this server yet" hint="This App Factory does not manage skill sources yet." />
      ) : (
        <Tabs defaultValue="installed">
          <TabsList className="mb-4">
            <TabsTrigger value="installed"><Sparkles className="size-4" />Installed</TabsTrigger>
            <TabsTrigger value="library"><BookOpen className="size-4" />Library</TabsTrigger>
            <TabsTrigger value="sources"><FolderGit2 className="size-4" />Sources</TabsTrigger>
          </TabsList>
          <TabsContent value="installed"><Installed manage={manage} base={base} /></TabsContent>
          <TabsContent value="library"><Library manage={manage} base={base} /></TabsContent>
          <TabsContent value="sources"><Sources manage={manage} /></TabsContent>
        </Tabs>
      )}
    </>
  );
}
