"use client";

import Link from "@/components/app/link";
import { ChevronRight } from "lucide-react";
import { PageHeader } from "@/components/app/page";
import { useNav } from "@/components/app/shell";

export default function Page() {
  const nav = useNav();
  return (
    <>
      <PageHeader title="More" />
      <div className="divide-y rounded-xl border bg-card">
        {nav.filter((n) => ["/library", "/chat", "/costs", "/nodes", "/teams", "/settings", "/companies"].includes(n.href)).map((n) => (
          <Link key={n.href} href={n.href} className="flex items-center gap-3 px-4 py-3.5">
            <n.icon className="size-5 text-muted-foreground" /><span className="flex-1">{n.label}</span><ChevronRight className="size-4 text-muted-foreground" />
          </Link>
        ))}
      </div>
    </>
  );
}
