import { Check } from "lucide-react";
import { Fragment, type ReactNode } from "react";
import { cn } from "@/lib/utils";

// Markdown: a small, safe renderer for agent text and markdown files (no HTML passthrough, no remote images):
// headings, paragraphs, nested and task lists, block quotes, fenced code, tables, rules, and inline code,
// bold, italic, strikethrough and links. variant "document" is the reading layout of the materials viewer.
type Item = { text: string; checked?: boolean; sub: string[] };
type Align = "left" | "center" | "right" | undefined;
type Block =
  | { t: "h"; level: number; text: string }
  | { t: "p"; text: string }
  | { t: "code"; lang: string; text: string }
  | { t: "ul" | "ol"; items: Item[]; start: number }
  | { t: "quote"; text: string }
  | { t: "table"; head: string[]; align: Align[]; rows: string[][] }
  | { t: "hr" };

const indent = (l: string) => l.match(/^\s*/)?.[0].replace(/\t/g, "    ").length ?? 0;
const LIST = /^\s*([-*+]|\d+[.)])\s+/;
const isList = (l: string) => LIST.test(l);
const isOrdered = (l: string) => /^\s*\d+[.)]/.test(l);
const isRule = (l: string) => /^\s*([-*_])\s*\1\s*\1[\s\-*_]*$/.test(l);
const SEP = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;
const cells = (l: string) => {
  const s = l.trim().replace(/^\|/, "").replace(/(?<!\\)\|$/, "");
  return s.split(/(?<!\\)\|/).map((c) => c.trim().replace(/\\\|/g, "|"));
};
const isTable = (lines: string[], i: number) => lines[i].includes("|") && i + 1 < lines.length && SEP.test(lines[i + 1]) && lines[i + 1].includes("-");

function dedent(lines: string[]): string {
  const n = Math.min(...lines.filter((l) => l.trim()).map(indent));
  return lines.map((l) => l.replace(/\t/g, "    ").slice(Number.isFinite(n) ? n : 0)).join("\n");
}

function parse(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let i = 0;
  const special = (j: number) => /^\s*(```|~~~|#{1,6}\s|>)/.test(lines[j]) || isRule(lines[j]) || isList(lines[j]) || isTable(lines, j);
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }
    const fence = line.match(/^\s*(```+|~~~+)\s*([\w+-]*)/);
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
    if (isRule(line)) { out.push({ t: "hr" }); i++; continue; }
    if (isTable(lines, i)) {
      const head = cells(line);
      const align = cells(lines[i + 1]).map((c): Align => (c.startsWith(":") && c.endsWith(":") ? "center" : c.endsWith(":") ? "right" : c.startsWith(":") ? "left" : undefined));
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].trim() && lines[i].includes("|")) rows.push(cells(lines[i++]));
      out.push({ t: "table", head, align, rows });
      continue;
    }
    if (/^\s*>/.test(line)) {
      const body: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) body.push(lines[i++].replace(/^\s*>\s?/, ""));
      out.push({ t: "quote", text: body.join("\n") });
      continue;
    }
    if (isList(line)) {
      const base = indent(line);
      const ordered = isOrdered(line);
      const start = ordered ? parseInt(line, 10) || 1 : 1;
      const items: Item[] = [];
      while (i < lines.length) {
        const l = lines[i];
        if (!l.trim()) {
          const next = lines.slice(i + 1).find((x) => x.trim());
          if (next !== undefined && items.length && (indent(next) > base || (isList(next) && indent(next) === base && isOrdered(next) === ordered))) {
            items[items.length - 1].sub.push("");
            i++;
            continue;
          }
          break;
        }
        if (isList(l) && indent(l) === base) {
          if (isOrdered(l) !== ordered) break;
          let text = l.replace(LIST, "");
          let checked: boolean | undefined;
          const task = text.match(/^\[([ xX])\]\s+/);
          if (task) { checked = task[1] !== " "; text = text.slice(task[0].length); }
          items.push({ text, checked, sub: [] });
          i++;
          continue;
        }
        const last = items[items.length - 1];
        if (last && indent(l) > base) { last.sub.push(l); i++; continue; }
        // A lazy continuation line of the item's text.
        if (last && !last.sub.length && !special(i) && indent(l) <= base && lines[i - 1].trim()) { last.text += " " + l.trim(); i++; continue; }
        break;
      }
      out.push({ t: ordered ? "ol" : "ul", items, start });
      continue;
    }
    const body: string[] = [];
    while (i < lines.length && lines[i].trim() && !(body.length && special(i))) body.push(lines[i++]);
    out.push({ t: "p", text: body.join("\n") });
  }
  return out;
}

const linkClass = "font-medium text-blue-600 underline decoration-blue-600/30 underline-offset-2 break-words hover:decoration-blue-600 dark:text-blue-400";

// inline renders `code`, **bold**, *italic*/_italic_, ~~strike~~ and [links](https://…) (an image's alt
// text becomes its link); everything else is text.
function inline(text: string, key = ""): ReactNode[] {
  const re = /(`+)([\s\S]+?)\1|\*\*([\s\S]+?)\*\*|__([\s\S]+?)__|(?<![\w*])\*(?!\s)([^*\n]+?)\*(?![\w*])|(?<!\w)_(?!\s)([^_\n]+?)_(?!\w)|~~([^~\n]+)~~|!?\[([^\]\n]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/g;
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
    else if (m[7] !== undefined) out.push(<s key={k} className="text-muted-foreground">{inline(m[7], k)}</s>);
    else {
      const href = m[9] ?? m[10];
      const safe = /^(https?:|mailto:|\/)/.test(href);
      out.push(safe
        ? <a key={k} href={href} target="_blank" rel="noreferrer noopener" className={cn(linkClass, !m[8] && "break-all")}>{m[8] ? inline(m[8], k) : href}</a>
        : <span key={k}>{m[8] ?? href}</span>);
    }
    last = at + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out.map((x, i) => (typeof x === "string" ? <Fragment key={`${key}t${i}`}>{x}</Fragment> : x));
}

export function CodeBlock({ text, lang, className, tall }: { text: string; lang?: string; className?: string; tall?: boolean }) {
  return (
    <div className={cn("group relative overflow-hidden rounded-lg border bg-muted/50", className)}>
      {lang && <div className="border-b px-3 py-1 font-mono text-[11px] uppercase tracking-wide text-muted-foreground">{lang}</div>}
      <pre className={cn("overflow-auto p-3 font-mono text-[12.5px] leading-relaxed", !tall && "max-h-[28rem]")}><code>{text}</code></pre>
    </div>
  );
}

type Variant = "compact" | "document";

const headingClass = (level: number, v: Variant) => v === "document"
  ? cn("font-semibold tracking-tight text-balance", level === 1 ? "mt-2 text-2xl sm:text-[28px] sm:leading-tight" : level === 2 ? "mt-6 border-b pb-2 text-xl" : level === 3 ? "mt-4 text-lg" : "mt-3 text-base")
  : cn("pt-1 font-semibold tracking-tight", level === 1 ? "text-lg" : level === 2 ? "text-base" : "text-[15px] md:text-sm");

function List({ b, k, variant }: { b: Extract<Block, { t: "ul" | "ol" }>; k: string; variant: Variant }) {
  const tasks = b.items.some((it) => it.checked !== undefined);
  const Tag = b.t;
  return (
    <Tag start={b.t === "ol" ? b.start : undefined}
      className={cn("space-y-1", tasks ? "pl-1" : "pl-5 marker:text-muted-foreground", !tasks && (b.t === "ol" ? "list-decimal" : "list-disc"))}>
      {b.items.map((it, j) => (
        <li key={j} className={cn(it.checked !== undefined && "flex list-none items-start gap-2")}>
          {it.checked !== undefined && (
            <span aria-label={it.checked ? "done" : "not done"} className={cn("mt-[0.3em] flex size-4 shrink-0 items-center justify-center rounded border",
              it.checked ? "border-emerald-600 bg-emerald-600 text-white" : "border-muted-foreground/40")}>
              {it.checked && <Check className="size-3" strokeWidth={3} />}
            </span>
          )}
          <span className={cn("min-w-0", it.checked !== undefined && "flex-1", it.checked && "text-muted-foreground")}>
            {inline(it.text, `${k}.${j}`)}
            {it.sub.some((l) => l.trim()) && <Markdown text={dedent(it.sub)} variant={variant} className="mt-1 space-y-1" />}
          </span>
        </li>
      ))}
    </Tag>
  );
}

export function Markdown({ text, className, variant = "compact" }: { text: string; className?: string; variant?: Variant }) {
  const blocks = parse(text);
  const doc = variant === "document";
  return (
    <div className={cn(doc ? "space-y-4 text-[15px] leading-7 break-words" : "space-y-3 text-[15px] leading-relaxed break-words md:text-sm", className)}>
      {blocks.map((b, i) => {
        const k = String(i);
        switch (b.t) {
          case "h":
            return <p key={k} role="heading" aria-level={b.level} className={headingClass(b.level, variant)}>{inline(b.text, k)}</p>;
          case "p":
            return <p key={k} className="whitespace-pre-line">{inline(b.text, k)}</p>;
          case "code":
            return <CodeBlock key={k} text={b.text} lang={b.lang} tall={doc} />;
          case "quote":
            return <blockquote key={k} className={cn("border-l-2 pl-3 text-muted-foreground", doc && "border-l-[3px] pl-4")}><Markdown text={b.text} variant={variant} /></blockquote>;
          case "hr":
            return <hr key={k} className="border-border" />;
          case "ul":
          case "ol":
            return <List key={k} b={b} k={k} variant={variant} />;
          case "table":
            return (
              <div key={k} className="overflow-x-auto rounded-lg border">
                <table className="w-full border-collapse text-left text-sm leading-normal">
                  <thead className="bg-muted/50">
                    <tr>{b.head.map((c, j) => <th key={j} scope="col" style={{ textAlign: b.align[j] }} className="border-b px-3 py-2 font-semibold whitespace-nowrap">{inline(c, `${k}.h${j}`)}</th>)}</tr>
                  </thead>
                  <tbody>
                    {b.rows.map((r, ri) => (
                      <tr key={ri} className="border-b last:border-0">
                        {b.head.map((h, j) => <td key={j} style={{ textAlign: b.align[j] }} className={cn("px-3 py-2 align-top", h.length > 3 && "min-w-36")}>{inline(r[j] ?? "", `${k}.${ri}.${j}`)}</td>)}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
        }
      })}
    </div>
  );
}
