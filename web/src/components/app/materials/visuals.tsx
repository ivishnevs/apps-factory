"use client";

import { Maximize2 } from "lucide-react";
import { useMemo, useState } from "react";
import type { MaterialRef, MessageAttachment } from "@/lib/api/types";
import type { Material } from "@/lib/api/types-materials";
import { useNow } from "@/components/app/run/use-now";
import { cn } from "@/lib/utils";
import { MaterialRow } from "./material-row";
import { messageVisuals, type Visual } from "./message-visuals";
import { MaterialViewer } from "./viewer";

// MessageVisuals (inbox-visual-previews) shows what a message or a decision proposal carries where the person
// answers: images inline in the order given with their captions — several side by side (a swipe on a phone), so
// "classic | v1 | v2" needs no composite — and other files as material rows. A click opens the viewer full size
// with the original to download.
export function MessageVisuals({ project, at, author, attachments, materials, className }: {
  project: string; at: number; author: string; attachments?: MessageAttachment[]; materials?: MaterialRef[]; className?: string;
}) {
  const items = useMemo(() => messageVisuals(project, at, author, attachments, materials), [project, at, author, attachments, materials]);
  const [open, setOpen] = useState<Material | null>(null);
  const now = Math.floor(useNow(60_000) / 1000);
  if (items.length === 0) return null;
  const images = items.filter((v) => v.thumb);
  const others = items.filter((v) => !v.thumb);
  return (
    <div className={cn("min-w-0 space-y-2", className)}>
      {images.length > 0 && <Images images={images} onOpen={setOpen} />}
      {others.length > 0 && (
        <div className="divide-y overflow-hidden rounded-xl border bg-card text-card-foreground">
          {others.map((v) => (
            <MaterialRow key={v.material.id} m={v.material} now={now} onOpen={setOpen} title={v.caption ? `${v.caption} · ${v.material.name}` : undefined} className="py-2.5" />
          ))}
        </div>
      )}
      <MaterialViewer material={open} onClose={() => setOpen(null)} now={now} showTask={false} />
    </div>
  );
}

function Images({ images, onOpen }: { images: Visual[]; onOpen: (m: Material) => void }) {
  const several = images.length > 1;
  return (
    <div role="list" aria-label={several ? "Images side by side" : "Image"}
      className={cn(several
        ? "-mx-1 flex snap-x snap-mandatory gap-2 overflow-x-auto px-1 pb-1 sm:mx-0 sm:grid sm:overflow-visible sm:px-0 sm:pb-0"
        : "flex",
      several && (images.length === 2 ? "sm:grid-cols-2" : images.length === 3 ? "sm:grid-cols-3" : "sm:grid-cols-4"))}>
      {images.map((v) => (
        <figure key={v.material.id} role="listitem" className={cn("min-w-0", several ? "w-[78%] shrink-0 snap-start sm:w-auto" : "w-full max-w-xl")}>
          <button type="button" onClick={() => onOpen(v.material)} aria-label={`Open ${v.caption || v.material.name} full size`}
            className="group relative block w-full overflow-hidden rounded-xl border bg-[repeating-conic-gradient(#8881_0_25%,transparent_0_50%)] bg-[length:16px_16px] focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={v.thumb} alt={v.caption || v.material.name} loading="lazy"
              className={cn("block w-full object-contain", several ? "h-44 sm:h-52" : "max-h-96")} />
            <span className="absolute top-1.5 right-1.5 hidden rounded-md bg-background/80 p-1 text-muted-foreground group-hover:block"><Maximize2 className="size-3.5" /></span>
          </button>
          <figcaption className="mt-1 truncate px-0.5 text-xs" title={v.material.path || v.material.name}>
            {v.caption ? <span className="font-medium">{v.caption}</span> : <span className="text-muted-foreground">{v.material.name}</span>}
          </figcaption>
        </figure>
      ))}
    </div>
  );
}
