"use client";

import { Bot } from "lucide-react";
import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { useMe } from "@/lib/api/client";
import { welcomeSeenKey } from "@/lib/company";

// After sign-in (saas-web-ui D7): the first time, the setup wizard of the person's company; afterwards the company.
// "Seen" is kept in this browser for now (a flag on the user in Core is a later step).
export default function Welcome() {
  const me = useMe();
  const router = useRouter();
  useEffect(() => {
    if (!me.data) return;
    const ids = me.data.companies.map((c) => c.company);
    const company = me.data.last_company && ids.includes(me.data.last_company) ? me.data.last_company : ids[0];
    if (!company) { router.replace("/signin?error=no_company"); return; }
    let seen = false;
    try { seen = !!localStorage.getItem(welcomeSeenKey(me.data.user?.id ?? me.data.kind)); } catch { /* storage blocked */ }
    router.replace(`/c/${encodeURIComponent(company)}${seen ? "" : "/setup?welcome=1"}`);
  }, [me.data, router]);
  return (
    <div className="flex min-h-dvh items-center justify-center text-muted-foreground">
      <Bot className="size-6 animate-pulse" aria-label="Loading" />
    </div>
  );
}
