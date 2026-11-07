"use client";

import { useQueryClient } from "@tanstack/react-query";
import { Pencil } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { core } from "@/lib/api/client";

// RenameTeam (structured-catalog 1.3): a team's display name is one field of its entity; references keep its name.
export function RenameTeam({ team, title }: { team: string; title?: string }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState(title ?? team);
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try {
      const es = await core<{ kind: string; name: string; row_version: number }[]>("/catalog/entities");
      let e = es?.find((x) => x.kind === "team" && x.name === team);
      if (!e) {
        await core("/catalog/entities/import", { method: "POST", body: "{}" });
        e = (await core<{ kind: string; name: string; row_version: number }[]>("/catalog/entities"))?.find((x) => x.kind === "team" && x.name === team);
      }
      if (!e) throw new Error("team not found in the configuration");
      await core(`/catalog/entities/team/${encodeURIComponent(team)}`, { method: "PUT", body: JSON.stringify({ row_version: e.row_version, title: value.trim() === team ? "" : value.trim(), note: `rename team ${team} to ${value.trim()}` }) });
      await qc.invalidateQueries();
      toast.success("Renamed");
      setOpen(false);
    } catch (err) { toast.error(err instanceof Error ? err.message : String(err)); } finally { setBusy(false); }
  };
  return (
    <>
      <Button variant="outline" className="h-10" onClick={() => setOpen(true)}><Pencil className="size-4" />Rename</Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader><DialogTitle>Rename team</DialogTitle><DialogDescription>The name people see. Agents and projects keep referring to it as {team}.</DialogDescription></DialogHeader>
          <Input aria-label="Team name" value={value} onChange={(e) => setValue(e.target.value)} className="h-10" />
          <DialogFooter className="gap-2">
            <Button variant="outline" className="h-10" onClick={() => setOpen(false)}>Cancel</Button>
            <Button className="h-10" disabled={busy || !value.trim()} onClick={save}>Save</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
