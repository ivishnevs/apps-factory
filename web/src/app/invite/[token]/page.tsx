"use client";

import { useQuery } from "@tanstack/react-query";
import { Bot, Check, UserPlus } from "lucide-react";
import NextLink from "next/link";
import { useParams } from "next/navigation";
import { useState } from "react";
import { ErrorState } from "@/components/app/page";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { core } from "@/lib/api/client";
import type { Me } from "@/lib/api/types-account";

// An invitation link (team-invitations): it says which company invites, with which role and who sent it. Signed out,
// the person signs in from here (a new person then joins at once, without a company of their own); signed in, they
// join with one click. A used, revoked or expired link says so.
interface Preview { company: string; company_name: string; role: "admin" | "member"; invited_by: string; expires_at: number; open: boolean }

const roleWords = { admin: "an admin: teams, skills, secrets, nodes, projects", member: "a member: tasks, chats and approvals" };

async function getJSON<T>(path: string): Promise<T | null> {
  const res = await fetch(path, { credentials: "same-origin" });
  if (res.status === 401 || res.status === 404) return null;
  if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
  return (await res.json()) as T;
}

export default function InvitePage() {
  const { token } = useParams<{ token: string }>();
  const preview = useQuery({ queryKey: ["invitation", token], queryFn: () => getJSON<Preview>(`/auth/invitations/${encodeURIComponent(token)}`), retry: false });
  // Signed in or not, without the app's automatic redirect to the sign-in page.
  const me = useQuery({ queryKey: ["me-optional"], queryFn: () => getJSON<Me>("/api/core/me"), retry: false });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const p = preview.data;
  const member = p && me.data?.companies.find((c) => c.company === p.company);
  const join = async () => {
    setBusy(true); setError(null);
    try {
      const m = await core<{ company: string }>(`/me/invitations/${encodeURIComponent(token)}`, { method: "POST", body: "{}" });
      window.location.assign(`/c/${encodeURIComponent(m.company)}`);
    } catch (e) { setError(e); setBusy(false); }
  };
  const signIn = `/signin?return=${encodeURIComponent(`/invite/${token}`)}`;
  return (
    <main className="flex min-h-dvh items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <span className="flex size-12 items-center justify-center rounded-xl bg-primary text-primary-foreground"><Bot className="size-6" /></span>
          <h1 className="text-2xl font-semibold tracking-tight">{p && p.open && !member ? `Join ${p.company_name}` : p ? p.company_name : "Invitation"}</h1>
        </div>
        {preview.error || me.error ? <ErrorState error={preview.error ?? me.error} /> : preview.isPending || me.isPending ? <Skeleton className="h-48 rounded-2xl" /> : !p ? (
          <Card>
            <p>This invitation link is not valid. Check that you copied all of it, or ask for a new one.</p>
            <Button asChild variant="outline" className="h-10 w-full"><NextLink href="/">Go to App Factory</NextLink></Button>
          </Card>
        ) : member ? (
          <Card>
            <p className="flex items-center gap-2 text-foreground"><Check className="size-4 text-emerald-600" />You are already in {p.company_name}.</p>
            <Button asChild className="h-10 w-full"><NextLink href={`/c/${encodeURIComponent(p.company)}`}>Open {p.company_name}</NextLink></Button>
          </Card>
        ) : !p.open ? (
          <Card>
            <p>This link was already used, revoked or has expired. Ask {p.invited_by || "the person who sent it"} for a new one.</p>
          </Card>
        ) : (
          <Card>
            <p>{p.invited_by ? <span className="font-medium text-foreground">{p.invited_by}</span> : "An owner"} invites you to <span className="font-medium text-foreground">{p.company_name}</span> as {roleWords[p.role]}.</p>
            {error ? <ErrorState error={error} /> : null}
            {me.data?.user ? (
              <>
                <p className="text-xs">You are signed in as {me.data.user.email}.</p>
                <Button className="h-10 w-full" disabled={busy} onClick={join}><UserPlus className="size-4" />Join {p.company_name}</Button>
              </>
            ) : (
              <>
                <Button asChild className="h-10 w-full"><a href={signIn}><UserPlus className="size-4" />Sign in to join</a></Button>
                <p className="text-xs">New here? Signing in creates your account and joins you to {p.company_name}.</p>
              </>
            )}
          </Card>
        )}
      </div>
    </main>
  );
}

function Card({ children }: { children: React.ReactNode }) {
  return <div className="space-y-4 rounded-2xl border bg-card p-5 text-sm text-muted-foreground">{children}</div>;
}
