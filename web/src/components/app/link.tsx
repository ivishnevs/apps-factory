"use client";

import NextLink from "next/link";
import { usePathname, useRouter as useNextRouter } from "next/navigation";
import { type ComponentProps, useMemo } from "react";
import { appPath, companyHref, useCompany } from "@/lib/company";

// Link is next/link for app paths: "/tasks" goes to the page's company ("/c/acme/tasks").
export default function Link({ href, ...props }: ComponentProps<typeof NextLink>) {
  const company = useCompany();
  return <NextLink href={typeof href === "string" ? companyHref(company, href) : href} {...props} />;
}

// useRouter is next/navigation's router with push and replace for app paths.
export function useRouter() {
  const router = useNextRouter();
  const company = useCompany();
  return useMemo(() => ({
    ...router,
    push: (href: string, opts?: Parameters<typeof router.push>[1]) => router.push(companyHref(company, href), opts),
    replace: (href: string, opts?: Parameters<typeof router.replace>[1]) => router.replace(companyHref(company, href), opts),
  }), [router, company]);
}

// useAppPath is the pathname without the company prefix.
export const useAppPath = () => appPath(usePathname());
