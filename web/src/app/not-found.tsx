import { Bot } from "lucide-react";
import NextLink from "next/link";
import { Button } from "@/components/ui/button";

// Any address the web does not know.
export default function NotFound() {
  return (
    <main className="flex min-h-dvh flex-col items-center justify-center gap-3 px-4 text-center">
      <span className="flex size-12 items-center justify-center rounded-xl bg-muted"><Bot className="size-6" /></span>
      <h1 className="text-xl font-semibold">This page does not exist</h1>
      <p className="max-w-sm text-sm text-muted-foreground">The link may be old or mistyped.</p>
      <Button asChild className="mt-2"><NextLink href="/welcome">Go to your company</NextLink></Button>
    </main>
  );
}
