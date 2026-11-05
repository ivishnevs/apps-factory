"use client";

import { useQuery } from "@tanstack/react-query";
import { Check, ExternalLink, MessageCircle, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { ErrorState, LoadingRows } from "@/components/app/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ApiError, core } from "@/lib/api/client";

// Telegram (telegram-per-company): the company's own bot, added by an owner or admin with a token from @BotFather,
// and each person's own chat, connected with a one-time link. In the chat a reply answers that message, @name writes
// to a team lead and plain text answers the last message from an agent.
interface TelegramView { bot: { username: string; created_by: string; created_at: number } | null; environment?: boolean; me?: { connected: boolean; digest: boolean } }

const fail = (e: unknown) => toast.error(e instanceof Error ? e.message : String(e));

export function TelegramSettings({ manage }: { manage: boolean }) {
  const view = useQuery({ queryKey: ["telegram"], queryFn: () => core<TelegramView>("/telegram"), refetchInterval: (q) => (q.state.data?.me && !q.state.data.me.connected ? 3000 : false) });
  const confirm = useConfirm();
  const [token, setToken] = useState("");
  const [tokenError, setTokenError] = useState("");
  const [busy, setBusy] = useState(false);
  const [link, setLink] = useState("");
  if (view.error) return <ErrorState error={view.error} />;
  if (!view.data) return <LoadingRows rows={2} />;
  const { bot, me, environment } = view.data;
  const saveBot = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setTokenError("");
    try {
      const out = await core<{ username: string }>("/telegram/bot", { method: "PUT", body: JSON.stringify({ token: token.trim() }) });
      setToken(""); await view.refetch(); toast.success(`Bot @${out.username} added`);
    } catch (err) { if (err instanceof ApiError && err.field === "token") setTokenError(err.message); else fail(err); }
    finally { setBusy(false); }
  };
  const removeBot = async () => {
    if (!(await confirm({ title: `Remove @${bot?.username}?`, description: "Everyone's chats are disconnected and the bot stops answering. You can add a bot again later.", confirm: "Remove", destructive: true }))) return;
    try { await core("/telegram/bot", { method: "DELETE" }); setLink(""); await view.refetch(); toast.success("Bot removed"); } catch (err) { fail(err); }
  };
  const connect = async () => {
    try {
      const out = await core<{ url?: string; code: string }>("/telegram/connect", { method: "POST", body: "{}" });
      if (out.url) { setLink(out.url); window.open(out.url, "_blank", "noopener"); }
      else setLink(`/start ${out.code}`);
    } catch (err) { fail(err); }
  };
  const setDigest = async (digest: boolean) => {
    try { await core("/telegram/me", { method: "PATCH", body: JSON.stringify({ digest }) }); await view.refetch(); } catch (err) { fail(err); }
  };
  const disconnect = async () => {
    try { await core("/telegram/me", { method: "DELETE" }); setLink(""); await view.refetch(); toast.success("Telegram disconnected"); } catch (err) { fail(err); }
  };
  return (
    <div className="space-y-5">
      <div className="space-y-3">
        <h3 className="text-sm font-medium">The company&apos;s bot</h3>
        {bot ? (
          <div className="flex flex-wrap items-center gap-3 rounded-xl border px-4 py-3 text-sm">
            <MessageCircle className="size-4 text-muted-foreground" />
            <a className="flex-1 font-medium underline-offset-2 hover:underline" href={`https://t.me/${bot.username}`} target="_blank" rel="noreferrer">@{bot.username}</a>
            {manage && <Button variant="outline" size="sm" className="h-9" onClick={removeBot} aria-label={`Remove @${bot.username}`}><Trash2 className="size-4" />Remove</Button>}
          </div>
        ) : environment ? (
          <p className="text-sm text-muted-foreground">This company uses the bot set up on the server (FACTORY_TELEGRAM_TOKEN).</p>
        ) : manage ? (
          <form onSubmit={saveBot} className="space-y-3">
            <ol className="list-decimal space-y-1 pl-5 text-sm text-muted-foreground">
              <li>In Telegram open <a className="underline" href="https://t.me/BotFather" target="_blank" rel="noreferrer">@BotFather</a> and send <span className="font-mono">/newbot</span>.</li>
              <li>Choose its name, for example &ldquo;Acme Factory&rdquo;.</li>
              <li>Paste the token it gives you here. It is stored encrypted and never shown again.</li>
            </ol>
            <div className="flex max-w-xl gap-2">
              <Input value={token} onChange={(e) => { setToken(e.target.value); setTokenError(""); }} placeholder="123456789:AA…" aria-label="Bot token" aria-invalid={!!tokenError} autoComplete="off" className="font-mono" />
              <Button type="submit" className="h-10" disabled={busy || !token.trim()}>Add bot</Button>
            </div>
            {tokenError && <p className="text-sm text-destructive">{tokenError}</p>}
          </form>
        ) : <p className="text-sm text-muted-foreground">No bot yet: an owner or admin adds the company&apos;s Telegram bot here.</p>}
      </div>
      {me && (bot || environment) && (
        <div className="space-y-3 border-t pt-4">
          <h3 className="text-sm font-medium">Your Telegram</h3>
          {me.connected ? (
            <>
              <p className="flex items-center gap-2 text-sm"><Check className="size-4 text-emerald-600" />Connected. You get what needs you{manage ? "" : " (questions to the owner go to owners)"}.</p>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" className="size-4" checked={me.digest} onChange={(e) => setDigest(e.target.checked)} />
                Daily digest
              </label>
              <Button variant="outline" className="h-10" onClick={disconnect}>Disconnect</Button>
            </>
          ) : (
            <>
              <p className="text-sm text-muted-foreground">Get questions, alerts and the daily digest in Telegram and answer from there.</p>
              <Button className="h-10" onClick={connect}><ExternalLink className="size-4" />Connect Telegram</Button>
              {link && (link.startsWith("https://")
                ? <p className="text-sm text-muted-foreground">Telegram did not open? <a className="underline" href={link} target="_blank" rel="noreferrer">Open the link</a> and press Start. It works for 15 minutes.</p>
                : <p className="text-sm text-muted-foreground">Send <span className="font-mono text-foreground">{link}</span> to the company&apos;s bot within 15 minutes.</p>)}
            </>
          )}
          <div className="rounded-xl bg-muted/50 p-3 text-xs text-muted-foreground">
            <p className="font-medium text-foreground">In the chat</p>
            <ul className="mt-1 list-disc space-y-0.5 pl-4">
              <li>Reply to a message to answer exactly that one.</li>
              <li><span className="font-mono">@name text</span> writes to that team lead.</li>
              <li>Plain text answers the last message from an agent; the bot says where it went.</li>
              <li><span className="font-mono">/inbox</span> lists what waits for you, <span className="font-mono">/help</span> the rest.</li>
            </ul>
          </div>
        </div>
      )}
    </div>
  );
}
