"use client";

import { useQuery } from "@tanstack/react-query";
import { Bot, Check, Laptop, ShieldCheck, X } from "lucide-react";
import NextLink from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import { ErrorState } from "@/components/app/page";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { core, useMe } from "@/lib/api/client";
import { canManage } from "@/lib/api/types-account";
import { ago } from "@/lib/format";

// Approving a connector (node-install D5, saas-web-ui D8): `factory-node login` prints this page's address. After
// sign-in (a 401 sends the person to /signin and back), an owner or admin picks one of their companies, compares the
// code with the terminal and connects the machine; the terminal continues on its own.
interface NodeConnect { id: string; node_id: string; name: string; os: string; arch: string; address: string; fingerprint: string; code: string; status: string; company?: string; created_at: number; expires_at: number }

const systems: Record<string, string> = { darwin: "macOS", linux: "Linux" };

export default function ConnectPage() {
  const { id } = useParams<{ id: string }>();
  const me = useMe();
  const eligible = (me.data?.companies ?? []).filter((c) => canManage(c.role));
  const [company, setCompany] = useState("");
  useEffect(() => {
    if (company || eligible.length === 0) return;
    setCompany(eligible.find((c) => c.company === me.data?.last_company)?.company ?? eligible[0].company);
  }, [company, eligible, me.data?.last_company]);
  const headers = { "X-Factory-Company": company };
  const req = useQuery({ queryKey: ["node-connect", id, company], queryFn: () => core<NodeConnect>(`/node-connects/${encodeURIComponent(id)}`, { headers }), enabled: !!company, refetchInterval: 5000 });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const decide = async (what: "approve" | "reject") => {
    setBusy(true); setError(null);
    try { await core(`/node-connects/${encodeURIComponent(id)}/${what}`, { method: "POST", body: "{}", headers }); await req.refetch(); }
    catch (e) { setError(e); }
    finally { setBusy(false); }
  };
  const r = req.data;
  return (
    <main className="flex min-h-dvh items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <span className="flex size-12 items-center justify-center rounded-xl bg-primary text-primary-foreground"><Bot className="size-6" /></span>
          <h1 className="text-2xl font-semibold tracking-tight">Connect a machine</h1>
          <p className="text-sm text-muted-foreground">Approve only a machine where you just ran the App Factory installer.</p>
        </div>
        {me.error ? <ErrorState error={me.error} /> : !me.data ? <Skeleton className="h-64 rounded-2xl" /> : eligible.length === 0 ? (
          <p className="rounded-2xl border bg-card p-5 text-sm text-muted-foreground">Only owners and admins of a company can connect machines, and you are neither.</p>
        ) : (
          <div className="space-y-4 rounded-2xl border bg-card p-5">
            {eligible.length > 1 && (
              <label className="block space-y-1.5 text-sm"><span className="font-medium">Company</span>
                <select className="h-10 w-full rounded-md border bg-background px-2" value={company} onChange={(e) => setCompany(e.target.value)}>
                  {eligible.map((c) => <option key={c.company} value={c.company}>{c.name}</option>)}
                </select>
              </label>
            )}
            {req.error ? <ErrorState error={req.error} /> : !r ? <Skeleton className="h-40" /> : (
              <>
                <div className="flex items-center gap-3">
                  <span className="flex size-10 items-center justify-center rounded-full bg-muted"><Laptop className="size-5" /></span>
                  <div className="min-w-0">
                    <p className="truncate font-medium">{r.name}</p>
                    <p className="text-xs text-muted-foreground">{systems[r.os] ?? r.os} · {r.arch} · from {r.address || "unknown address"} · {ago(r.created_at)}</p>
                  </div>
                </div>
                <div className="rounded-lg border bg-muted/30 p-3 text-center">
                  <p className="text-xs text-muted-foreground">The terminal shows the code</p>
                  <p className="mt-1 font-mono text-2xl font-semibold tracking-widest">{r.code}</p>
                </div>
                <p className="break-all text-xs text-muted-foreground">Fingerprint <span className="font-mono">{r.fingerprint}</span></p>
                {error ? <ErrorState error={error} /> : null}
                {r.status === "pending" ? (
                  <div className="flex gap-2">
                    <Button variant="outline" className="h-10 flex-1" disabled={busy} onClick={() => decide("reject")}><X className="size-4" />Cancel</Button>
                    <Button className="h-10 flex-1" disabled={busy} onClick={() => decide("approve")}><ShieldCheck className="size-4" />Connect</Button>
                  </div>
                ) : r.status === "approved" ? (
                  <div className="space-y-3">
                    <p className="flex items-center gap-2 text-sm text-emerald-700 dark:text-emerald-400"><Check className="size-4" />Connected to {eligible.find((c) => c.company === r.company)?.name ?? r.company}. The terminal continues on its own.</p>
                    <Button asChild className="h-10 w-full"><NextLink href={`/c/${encodeURIComponent(r.company ?? company)}/nodes`}>See your connectors</NextLink></Button>
                  </div>
                ) : (
                  <p className="text-sm text-muted-foreground">{r.status === "expired" ? "This request expired. Run the installer (or factory-node login) again." : "This request was cancelled."}</p>
                )}
              </>
            )}
          </div>
        )}
      </div>
    </main>
  );
}
