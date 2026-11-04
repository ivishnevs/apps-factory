"use client";

import { AlertTriangle } from "lucide-react";
import { useEffect } from "react";
import { Button } from "@/components/ui/button";

// A page that broke while rendering: say so, keep the way back, and let the person retry instead of a blank screen.
export default function Error({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  useEffect(() => { console.error(error); }, [error]);
  return (
    <main className="flex min-h-[60dvh] flex-col items-center justify-center gap-3 px-4 text-center">
      <span className="flex size-12 items-center justify-center rounded-xl bg-red-500/10 text-red-600"><AlertTriangle className="size-6" /></span>
      <h1 className="text-xl font-semibold">Something went wrong on this page</h1>
      <p className="max-w-sm text-sm text-muted-foreground">Your data is safe. Try again; if it keeps happening, reload the page.{error.digest ? ` (ref ${error.digest})` : ""}</p>
      <div className="mt-2 flex gap-2">
        <Button onClick={reset}>Try again</Button>
        <Button variant="outline" onClick={() => window.location.reload()}>Reload</Button>
      </div>
    </main>
  );
}
