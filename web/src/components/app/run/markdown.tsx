import { Fragment, type ReactNode } from "react";
import { cn } from "@/lib/utils";

// Markdown: a small, safe renderer for agent text and markdown artifacts (no HTML passthrough): headings,
// paragraphs, lists, block quotes, fenced code, rules, and inline code, bold, italic and links.
type Block =
  | { t: "h"; level: number; text: string }
  | { t: "p"; text: string }
  | { t: "code"; lang: string; text: string }
  | { t: "ul" | "ol"; items: string[]; start: number }
  | { t: "quote"; text: string }
  | { t: "hr" };

function parse(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let i = 0;
  const isList = (l: string) => /^\s*([-*+]|\d+[.)])\s+/.test(l);
  const special = (l: string) => /^\s*(```|~~~|#{1,6}\s|>|([-*_])\s*\2\s*\2[\s\-*_]*$)/.test(l) || isList(l);
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }
    const fence = line.match(/^\s*(```|~~~)\s*([\w+-]*)/);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
      i++;
      out.push({ t: "code", lang: fence[2], text: body.join("\n") });
      continue;
    }
    const h = line.match(/^\s*(#{1,6})\s+(.*)$/);
    if (h) { out.push({ t: "h", level: h[1].length, text: h[2].replace(/\s#+\s*$/, "") }); i++; continue; }
    if (/^\s*([-*_])\s*\1\s*\1[\s\-*_]*$/.test(line)) { out.push({ t: "hr" }); i++; continue; }
    if (/^\s*>/.test(line)) {
      const body: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) body.push(lines[i++].replace(/^\s*>\s?/, ""));
      out.push({ t: "quote", text: body.join("\n") });
      continue;
    }
    if (isList(line)) {
      const ordered = /^\s*\d+[.)]/.test(line);
      const start = ordered ? parseInt(line, 10) || 1 : 1;
      const items: string[] = [];
      while (i < lines.length && (isList(lines[i]) || (lines[i].trim() && /^\s{2,}/.test(lines[i]) && items.length))) {
        if (isList(lines[i])) items.push(lines[i].replace(/^\s*([-*+]|\d+[.)])\s+/, ""));
        else items[items.length - 1] += " " + lines[i].trim();
        i++;
      }
      out.push({ t: ordered ? "ol" : "ul", items, start });
      continue;
    }
    const body: string[] = [];
    while (i < lines.length && lines[i].trim() && !(body.length && special(lines[i]))) body.push(lines[i++]);
    out.push({ t: "p", text: body.join("\n") });
  }
  return out;
}

// inline renders `code`, **bold**, *italic*/_italic_ and [links](https://…); everything else is text.
function inline(text: string, key = ""): ReactNode[] {
  const re = /(`+)([\s\S]+?)\1|\*\*([\s\S]+?)\*\*|__([\s\S]+?)__|(?<![\w*])\*(?!\s)([^*\n]+?)\*(?![\w*])|(?<!\w)_(?!\s)([^_\n]+?)_(?!\w)|\[([^\]\n]+)\]\(([^)\s]+)\)|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/g;
  const out: ReactNode[] = [];
  let last = 0;
  let n = 0;
  for (const m of text.matchAll(re)) {
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    const k = `${key}.${n++}`;
    if (m[2] !== undefined) out.push(<code key={k} className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-[0.85em] break-words">{m[2]}</code>);
    else if (m[3] !== undefined || m[4] !== undefined) out.push(<strong key={k} className="font-semibold">{inline(m[3] ?? m[4], k)}</strong>);
    else if (m[5] !== undefined || m[6] !== undefined) out.push(<em key={k}>{inline(m[5] ?? m[6], k)}</em>);
    else {
      const href = m[8] ?? m[9];
      const safe = /^(https?:|mailto:|\/)/.test(href);
      out.push(safe
        ? <a key={k} href={href} target="_blank" rel="noreferrer noopener" className="font-medium text-blue-600 underline decoration-blue-600/30 underline-offset-2 break-all hover:decoration-blue-600 dark:text-blue-400">{m[7] ?? href}</a>
        : <span key={k}>{m[7] ?? href}</span>);
    }
    last = at + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out.map((x, i) => (typeof x === "string" ? <Fragment key={`${key}t${i}`}>{x}</Fragment> : x));
}

export function CodeBlock({ text, lang, className }: { text: string; lang?: string; className?: string }) {
  return (
    <div className={cn("group relative overflow-hidden rounded-lg border bg-muted/50", className)}>
      {lang && <div className="border-b px-3 py-1 font-mono text-[11px] uppercase tracking-wide text-muted-foreground">{lang}</div>}
      <pre className="max-h-[28rem] overflow-auto p-3 font-mono text-[12.5px] leading-relaxed"><code>{text}</code></pre>
    </div>
  );
}

export function Markdown({ text, className }: { text: string; className?: string }) {
  const blocks = parse(text);
  return (
    <div className={cn("space-y-3 text-[15px] leading-relaxed break-words md:text-sm", className)}>
      {blocks.map((b, i) => {
        const k = String(i);
        switch (b.t) {
          case "h": {
            const size = b.level === 1 ? "text-lg" : b.level === 2 ? "text-base" : "text-[15px] md:text-sm";
            return <p key={k} role="heading" aria-level={b.level} className={cn("pt-1 font-semibold tracking-tight", size)}>{inline(b.text, k)}</p>;
          }
          case "p":
            return <p key={k} className="whitespace-pre-line">{inline(b.text, k)}</p>;
          case "code":
            return <CodeBlock key={k} text={b.text} lang={b.lang} />;
          case "quote":
            return <blockquote key={k} className="border-l-2 pl-3 text-muted-foreground"><Markdown text={b.text} /></blockquote>;
          case "hr":
            return <hr key={k} className="border-border" />;
          case "ul":
            return <ul key={k} className="list-disc space-y-1 pl-5 marker:text-muted-foreground">{b.items.map((it, j) => <li key={j}>{inline(it, `${k}.${j}`)}</li>)}</ul>;
          case "ol":
            return <ol key={k} start={b.start} className="list-decimal space-y-1 pl-5 marker:text-muted-foreground">{b.items.map((it, j) => <li key={j}>{inline(it, `${k}.${j}`)}</li>)}</ol>;
        }
      })}
    </div>
  );
}
