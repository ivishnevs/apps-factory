"use client";

import { Bot, LogIn } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { Suspense } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";

// Sign-in (user-accounts 6.1): one button per provider Core offers (Dex connectors). Core runs the OIDC flow
// (/auth/login → provider → /auth/callback) and comes back to the page the person started from.
const providerWords: Record<string, { label: string; icon?: typeof LogIn }> = {
  google: { label: "Continue with Google" },
  github: { label: "Continue with GitHub" },
  mock: { label: "Continue as the test user" },
};

const errors: Record<string, string> = {
  state: "The sign-in took too long or was opened in another browser. Please try again.",
  provider: "The provider did not complete the sign-in. Please try again.",
  email_unverified: "This email belongs to another account and the provider has not verified it. Sign in the way you did before.",
  account: "We could not set up your account. Please try again in a minute.",
  session: "We could not start your session. Please try again.",
  unavailable: "Sign-in is unavailable right now. Please try again in a minute.",
  no_company: "You are not in any company yet. Ask a company owner for an invite link.",
};

function SignIn() {
  const params = useSearchParams();
  const providers = useQuery({
    queryKey: ["auth-providers"],
    queryFn: async () => {
      const res = await fetch("/auth/providers", { credentials: "same-origin" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      return (await res.json()) as { providers: string[]; status_note?: string };
    },
    retry: false,
  });
  const back = params.get("return") || "/welcome";
  const error = params.get("error");
  return (
    <main className="flex min-h-dvh items-center justify-center px-4 py-10">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <span className="flex size-12 items-center justify-center rounded-xl bg-primary text-primary-foreground"><Bot className="size-6" /></span>
          <h1 className="text-2xl font-semibold tracking-tight">Sign in to App Factory</h1>
          <p className="text-sm text-muted-foreground">Your AI company at work. New here? Signing in creates your company.</p>
        </div>
        {providers.data?.status_note && <Alert className="mb-4"><AlertDescription>{providers.data.status_note}</AlertDescription></Alert>}
        {params.get("expired") &&<Alert className="mb-4"><AlertDescription>Your session ended. Sign in again to continue.</AlertDescription></Alert>}
        {error && <Alert variant="destructive" className="mb-4"><AlertDescription>{errors[error] ?? "The sign-in did not work. Please try again."}</AlertDescription></Alert>}
        <div className="flex flex-col gap-3">
          {providers.isLoading && <><Skeleton className="h-11" /><Skeleton className="h-11" /></>}
          {providers.error && <p className="text-center text-sm text-muted-foreground">Sign-in is not available on this server.</p>}
          {providers.data?.providers.length === 0 && <p className="text-center text-sm text-muted-foreground">Sign-in is not configured on this server.</p>}
          {providers.data?.providers.map((p) => {
            const w = providerWords[p] ?? { label: `Continue with ${p}` };
            const Icon = w.icon ?? LogIn;
            const href = `/auth/login?provider=${encodeURIComponent(p)}${back ? `&return=${encodeURIComponent(back)}` : ""}`;
            return (
              <Button key={p} asChild variant="outline" size="lg" className="h-11 justify-center gap-2 text-sm">
                <a href={href}><Icon className="size-4" />{w.label}</a>
              </Button>
            );
          })}
        </div>
      </div>
    </main>
  );
}

export default function Page() {
  return <Suspense><SignIn /></Suspense>;
}
