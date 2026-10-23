"use client";

import { createContext, useCallback, useContext, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";

// useConfirm: every owner action asks first (owner-ui rule). await confirm({title, description}) → boolean.
interface Ask { title: string; description?: string; confirm?: string; destructive?: boolean }
const Ctx = createContext<(a: Ask) => Promise<boolean>>(async () => false);
export const useConfirm = () => useContext(Ctx);

export function ConfirmProvider({ children }: { children: React.ReactNode }) {
  const [ask, setAsk] = useState<Ask | null>(null);
  const done = useRef<(v: boolean) => void>(() => {});
  const confirm = useCallback((a: Ask) => new Promise<boolean>((resolve) => { done.current = resolve; setAsk(a); }), []);
  const close = (v: boolean) => { done.current(v); setAsk(null); };
  return (
    <Ctx.Provider value={confirm}>
      {children}
      <Dialog open={!!ask} onOpenChange={(o) => !o && close(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{ask?.title}</DialogTitle>
            {ask?.description && <DialogDescription className="whitespace-pre-wrap">{ask.description}</DialogDescription>}
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => close(false)}>Cancel</Button>
            <Button variant={ask?.destructive ? "destructive" : "default"} onClick={() => close(true)}>{ask?.confirm ?? "Confirm"}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Ctx.Provider>
  );
}
