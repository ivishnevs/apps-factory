"use client";

import { Bot } from "lucide-react";
import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { useMe } from "@/lib/api/client";

// The start page sends a signed-in person to their last company (user-accounts D6), else to the sign-in.
export default function Start() {
  const me = useMe();
  const router = useRouter();
  useEffect(() => {
    if (me.error) { router.replace("/signin"); return; }
    if (!me.data) return;
    const ids = me.data.companies.map((c) => c.company);
    const target = me.data.last_company && ids.includes(me.data.last_company) ? me.data.last_company : ids[0];
    router.replace(target ? `/c/${encodeURIComponent(target)}` : "/signin?error=no_company");
  }, [me.data, me.error, router]);
  return (
    <div className="flex min-h-dvh items-center justify-center text-muted-foreground">
      <Bot className="size-6 animate-pulse" aria-label="Loading" />
    </div>
  );
}
