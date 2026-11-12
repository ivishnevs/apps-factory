"use client";

import { ArrowRight, Bot, CheckCircle2, Cpu, ShieldCheck, Users } from "lucide-react";
import NextLink from "next/link";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import type { Me } from "@/lib/api/types-account";

// The landing page (saas-web-ui D5): what App Factory is and the way in. A signed-in visitor gets a button to their
// company instead of the sign-in. It asks /api/core/me itself so a 401 does not bounce the visitor to the sign-in.
function useSignedIn() {
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  useEffect(() => {
    fetch("/api/core/me", { credentials: "same-origin" })
      .then((r) => (r.ok ? r.json() : null))
      .then((v: Me | null) => setMe(v && v.companies?.length ? v : null), () => setMe(null));
  }, []);
  return me;
}

const steps = [
  { icon: Users, title: "Pick a team", text: "A product owner, developers, QA, a designer — from presets you can tune: prompts, models, skills." },
  { icon: Cpu, title: "Connect your machine", text: "One command installs the node. Agents run there, on your Claude and Codex subscriptions." },
  { icon: Bot, title: "Give the lead a goal", text: "The lead plans, splits the work and hands it out; the team builds, reviews and tests." },
  { icon: ShieldCheck, title: "You decide", text: "Questions, approvals and merged pull requests come to your inbox. Costs and a kill switch are one click away." },
];

// Pricing (saas-billing, owner 2026-11-06; prices are revisited before launch): Core enforces the same limits.
const pricing = [
  { name: "Free", price: 0, lines: ["1 machine of your own (node)", "Just you", "Agents, projects and history not limited"] },
  { name: "Pro", price: 29, lines: ["5 machines of your own (nodes)", "Invite your team", "2,000 cloud credits a month (cloud nodes, coming soon)", "14 days free for new accounts, no card"] },
];

export default function Landing() {
  const me = useSignedIn();
  const company = me ? (me.last_company && me.companies.some((c) => c.company === me.last_company) ? me.last_company : me.companies[0].company) : "";
  return (
    <div className="min-h-dvh">
      <header className="mx-auto flex max-w-6xl items-center justify-between px-4 py-4 md:px-8">
        <span className="flex items-center gap-2 font-semibold"><span className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Bot className="size-4" /></span>App Factory</span>
        {me ? (
          <Button asChild className="h-9"><NextLink href={`/c/${encodeURIComponent(company)}`}>Open App Factory<ArrowRight className="size-4" /></NextLink></Button>
        ) : me === null ? (
          <Button asChild variant="outline" className="h-9"><NextLink href="/signin?return=/welcome">Sign in</NextLink></Button>
        ) : <span className="h-9" />}
      </header>
      <main className="mx-auto max-w-6xl px-4 md:px-8">
        <section className="py-16 text-center md:py-24">
          <p className="mb-4 inline-flex items-center gap-2 rounded-full border px-3 py-1 text-xs text-muted-foreground"><CheckCircle2 className="size-3.5" />Agents on your own machine and subscriptions</p>
          <h1 className="mx-auto max-w-3xl text-4xl font-semibold tracking-tight md:text-6xl">Your AI company at work</h1>
          <p className="mx-auto mt-5 max-w-2xl text-base text-muted-foreground md:text-lg">
            App Factory runs a team of AI agents that plan, build, review and test your product — and brings you only the decisions.
          </p>
          <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            {me ? (
              <Button asChild size="lg" className="h-11"><NextLink href={`/c/${encodeURIComponent(company)}`}>Open App Factory<ArrowRight className="size-4" /></NextLink></Button>
            ) : (
              <Button asChild size="lg" className="h-11"><NextLink href="/signin?return=/welcome">Get started<ArrowRight className="size-4" /></NextLink></Button>
            )}
          </div>
        </section>
        <section className="grid gap-4 pb-20 sm:grid-cols-2 lg:grid-cols-4">
          {steps.map((s, i) => (
            <div key={s.title} className="rounded-2xl border bg-card p-5">
              <span className="mb-4 flex size-9 items-center justify-center rounded-full bg-muted"><s.icon className="size-4" /></span>
              <p className="text-xs text-muted-foreground">Step {i + 1}</p>
              <h2 className="mt-1 font-semibold">{s.title}</h2>
              <p className="mt-2 text-sm text-muted-foreground">{s.text}</p>
            </div>
          ))}
        </section>
        <section id="pricing" className="pb-20">
          <h2 className="text-center text-2xl font-semibold tracking-tight md:text-3xl">Pricing</h2>
          <p className="mx-auto mt-2 max-w-2xl text-center text-sm text-muted-foreground">Agents run on your machines and your own Claude and Codex subscriptions, so you never pay us for tokens. Paddle handles payment, taxes and invoices.</p>
          <div className="mx-auto mt-8 grid max-w-3xl gap-4 sm:grid-cols-2">
            {pricing.map((p) => (
              <div key={p.name} className="rounded-2xl border bg-card p-5">
                <h3 className="font-semibold">{p.name}</h3>
                <p className="mt-1"><span className="text-3xl font-semibold tracking-tight">${p.price}</span><span className="text-sm text-muted-foreground"> / month</span></p>
                <ul className="mt-4 space-y-2 text-sm">
                  {p.lines.map((l) => <li key={l} className="flex items-start gap-2"><CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-600" />{l}</li>)}
                </ul>
              </div>
            ))}
          </div>
        </section>
      </main>
      <footer className="border-t py-6 text-center text-xs text-muted-foreground">App Factory · <a href="/security" className="underline-offset-4 hover:underline">Security</a></footer>
    </div>
  );
}
