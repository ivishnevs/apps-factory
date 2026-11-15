"use client";

import { ImagePlus, Loader2, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { uploadImage } from "@/lib/api/client";
import type { MessageAttachment } from "@/lib/api/types";
import { cn } from "@/lib/utils";

// People's attachments (people-attachments): images a person adds to a chat message or a comment. Each file is
// uploaded to Core at once (POST /uploads); the message or comment names the uploads when it is sent.

export const MAX_ATTACHMENTS = 10;
export const MAX_ATTACHMENT_BYTES = 20 * 1024 * 1024;
const ACCEPT = "image/png,image/jpeg,image/gif,image/webp";
const accepted = (f: File) => ACCEPT.split(",").includes(f.type);

interface Item { key: string; name: string; url: string; att?: MessageAttachment; failed?: boolean }

export function useAttachments() {
  const [items, setItems] = useState<Item[]>([]);
  const urls = useRef<string[]>([]);
  useEffect(() => () => urls.current.forEach((u) => URL.revokeObjectURL(u)), []);

  const add = useCallback((files: File[]) => {
    const images = files.filter(accepted);
    if (images.length < files.length) toast.error("Only images can be attached: PNG, JPEG, GIF or WebP");
    setItems((cur) => {
      const room = MAX_ATTACHMENTS - cur.length;
      if (images.length > room) toast.error(`At most ${MAX_ATTACHMENTS} images`);
      const next: Item[] = [];
      for (const f of images.slice(0, Math.max(0, room))) {
        if (f.size > MAX_ATTACHMENT_BYTES) { toast.error(`${f.name}: an image up to 20 MB`); continue; }
        const key = `${Date.now()}-${Math.random()}`;
        const url = URL.createObjectURL(f);
        urls.current.push(url);
        next.push({ key, name: f.name, url });
        uploadImage(f).then(
          (att) => setItems((xs) => xs.map((x) => (x.key === key ? { ...x, att } : x))),
          (e: Error) => { toast.error(`${f.name}: ${e.message}`); setItems((xs) => xs.map((x) => (x.key === key ? { ...x, failed: true } : x))); },
        );
      }
      return [...cur, ...next];
    });
  }, []);
  const remove = (key: string) => setItems((xs) => xs.filter((x) => x.key !== key));
  const clear = () => setItems([]);
  const uploading = items.some((x) => !x.att && !x.failed);
  const ready = items.flatMap((x) => (x.att ? [x.att] : []));

  // Paste and drop: images on the clipboard or dragged onto the composer.
  const onPaste = (e: React.ClipboardEvent) => {
    const files = Array.from(e.clipboardData.files);
    if (files.length) { e.preventDefault(); add(files); }
  };
  const onDrop = (e: React.DragEvent) => {
    const files = Array.from(e.dataTransfer.files);
    if (files.length) { e.preventDefault(); add(files); }
  };
  const onDragOver = (e: React.DragEvent) => { if (e.dataTransfer.types.includes("Files")) e.preventDefault(); };
  return { items, add, remove, clear, uploading, ready, handlers: { onPaste, onDrop, onDragOver } };
}

export type Attachments = ReturnType<typeof useAttachments>;

// AttachButton opens the file picker.
export function AttachButton({ a, className, disabled }: { a: Attachments; className?: string; disabled?: boolean }) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <>
      <input ref={input} type="file" accept={ACCEPT} multiple hidden
        onChange={(e) => { a.add(Array.from(e.target.files ?? [])); e.target.value = ""; }} />
      <button type="button" aria-label="Attach images" title="Attach images (or paste, or drop them here)" disabled={disabled || a.items.length >= MAX_ATTACHMENTS}
        onClick={() => input.current?.click()}
        className={cn("flex size-10 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50", className)}>
        <ImagePlus className="size-5" />
      </button>
    </>
  );
}

// AttachmentTray shows the added images with their upload state and a remove button.
export function AttachmentTray({ a, className }: { a: Attachments; className?: string }) {
  if (a.items.length === 0) return null;
  return (
    <ul aria-label="Images to send" className={cn("flex flex-wrap gap-2", className)}>
      {a.items.map((x) => (
        <li key={x.key} className={cn("relative size-16 overflow-hidden rounded-lg border bg-muted", x.failed && "border-red-500/60")} title={x.name}>
          {/* eslint-disable-next-line @next/next/no-img-element -- a local object URL of the picked file */}
          <img src={x.url} alt={x.name} className={cn("size-full object-cover", !x.att && "opacity-60")} />
          {!x.att && !x.failed && <Loader2 aria-label="Uploading" className="absolute inset-0 m-auto size-5 animate-spin text-foreground" />}
          <button type="button" aria-label={`Remove ${x.name}`} onClick={() => a.remove(x.key)}
            className="absolute top-0.5 right-0.5 flex size-6 items-center justify-center rounded-full bg-background/90 text-foreground shadow-xs hover:bg-background">
            <X className="size-3.5" />
          </button>
        </li>
      ))}
    </ul>
  );
}
