"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useParams } from "next/navigation";
import { useEffect, useRef } from "react";
import { Building2 } from "lucide-react";
import { Shell } from "@/components/app/shell";
import { Button } from "@/components/ui/button";
import { ApiError, useCompanyInfo } from "@/lib/api/client";
import { CompanyContext, setCompany } from "@/lib/company";

// NotFound is a company the person is not a member of (Core answers 404 for those and for unknown ones alike).
function NotFound() {
  return (
    <main className="flex min-h-dvh flex-col items-center justify-center gap-3 px-4 text-center">
      <Building2 className="size-8 text-muted-foreground" />
      <h1 className="text-xl font-semibold">This company is not available</h1>
      <p className="max-w-sm text-sm text-muted-foreground">It does not exist or you are not a member of it.</p>
      <Button asChild className="mt-2"><a href="/">Go to your company</a></Button>
    </main>
  );
}

function Gate({ children }: { children: React.ReactNode }) {
  const info = useCompanyInfo();
  if (info.error instanceof ApiError && info.error.status === 404) return <NotFound />;
  return <Shell>{children}</Shell>;
}

// The signed-in app of one company (/c/{company}/…, user-accounts D6). The company is set before the first
// request of the page; switching companies drops every cached answer of the previous one.
export default function CompanyLayout({ children }: { children: React.ReactNode }) {
  const company = decodeURIComponent(String(useParams<{ company: string }>().company ?? ""));
  const qc = useQueryClient();
  const previous = useRef(company);
  setCompany(company);
  useEffect(() => {
    if (previous.current !== company) {
      previous.current = company;
      qc.clear();
    }
  }, [company, qc]);
  return (
    <CompanyContext.Provider value={company}>
      <Gate>{children}</Gate>
    </CompanyContext.Provider>
  );
}
