"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback } from "react";
import type { Material } from "@/lib/api/types-materials";

// useViewer keeps the open material in the URL (?file=<id>), so a material can be linked and the back button
// closes the viewer. Call it under a Suspense boundary (useSearchParams).
export function useViewer(items: Material[]) {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const id = params.get("file");
  const current = (id && items.find((m) => m.id === id)) || null;
  const go = useCallback((next: string | null, push: boolean) => {
    const p = new URLSearchParams(params.toString());
    if (next) p.set("file", next); else p.delete("file");
    const s = p.toString();
    const url = s ? `${pathname}?${s}` : pathname;
    if (push) router.push(url, { scroll: false }); else router.replace(url, { scroll: false });
  }, [params, pathname, router]);
  return {
    current,
    open: (m: Material) => go(m.id, true),
    close: () => go(null, false),
  };
}
