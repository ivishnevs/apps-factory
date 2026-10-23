"use client";

import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { useAction } from "@/lib/api/client";
import type { Action } from "@/lib/api/types";

interface Ask { title: string; description?: string; confirm?: string; destructive?: boolean }

// useOwnerAction asks first (useConfirm), then runs the action with a toast for the outcome. It resolves to
// whether the action was done.
export function useOwnerAction() {
  const confirm = useConfirm();
  const action = useAction();
  const run = async (a: Action, ask: Ask, done?: string) => {
    if (!(await confirm(ask))) return false;
    try {
      const r = await action.mutateAsync(a);
      toast.success(done ?? r.result.replace(/^done: /, "Done: "));
      return true;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return false;
    }
  };
  return { run, pending: action.isPending };
}
