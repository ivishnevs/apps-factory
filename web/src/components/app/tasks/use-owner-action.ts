"use client";

import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { useAction } from "@/lib/api/client";
import type { Action } from "@/lib/api/types";
import { describe } from "@/lib/api/types-tasks";

export interface AskOptions { title: string; confirm?: string; destructive?: boolean; description?: string }

// useOwnerAction asks for confirmation, runs the action and toasts the result; it resolves to the result
// text, or null when the owner cancelled or the action failed.
export function useOwnerAction() {
  const confirm = useConfirm();
  const action = useAction();
  const run = async (a: Action, ask: AskOptions): Promise<string | null> => {
    if (!(await confirm({ title: ask.title, description: ask.description ?? describe(a), confirm: ask.confirm, destructive: ask.destructive }))) return null;
    try {
      const { result } = await action.mutateAsync(a);
      toast.success(result ? result.charAt(0).toUpperCase() + result.slice(1) : "Done");
      return result ?? "";
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return null;
    }
  };
  return { run, pending: action.isPending };
}
