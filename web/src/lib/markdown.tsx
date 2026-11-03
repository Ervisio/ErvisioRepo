// A small, safe Markdown renderer (CommonMark subset plus GFM tables, task lists,
// strikethrough and autolinks). It produces React elements only: raw HTML in the
// source is shown as text, never parsed, and link targets are filtered by scheme.
import { Fragment, type ReactNode } from "react";

export type MarkdownContext = {
  /** Maps a relative link target to an in-app URL. */
  resolveLink?: (href: string) => string;
  /** Maps a relative image source to a same-origin URL; undefined renders the image as a link. */
  resolveImage?: (src: string) => string | undefined;
};

type Block =
  | { t: "h"; level: number; text: string }
  | { t: "p"; text: string }
  | { t: "code"; lang: string; text: string }
  | { t: "quote"; blocks: Block[] }
  | { t: "list"; ordered: boolean; start: number; items: { task?: boolean; checked?: boolean; blocks: Block[] }[] }
  | { t: "table"; align: ("left" | "center" | "right" | undefined)[]; head: string[]; rows: string[][] }
  | { t: "hr" };

const FENCE = /^ {0,3}(`{3,}|~{3,})\s*([^`\s]*)/;
const HEADING = /^ {0,3}(#{1,6})(?:\s+(.*?))?\s*#*\s*$/;
const HR = /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/;
const QUOTE = /^ {0,3}> ?/;
const LIST = /^( {0,3})([-*+]|\d{1,9}[.)])(\s+|$)/;
const TABLE_SEP = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;
const SETEXT = /^ {0,3}(=+|-+)\s*$/;

const blank = (s: string) => s.trim() === "";

function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const cells: string[] = [];
  let cur = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === "\\" && s[i + 1] === "|") {
      cur += "|";
      i++;
    } else if (c === "|") {
      cells.push(cur.trim());
      cur = "";
    } else cur += c;
  }
  cells.push(cur.trim());
  return cells;
}

function startsBlock(line: string, next: string | undefined): boolean {
  return (
    FENCE.test(line) || HEADING.test(line) || HR.test(line) || QUOTE.test(line) || LIST.test(line) ||
    (line.includes("|") && next !== undefined && TABLE_SEP.test(next) && next.includes("-"))
  );
}

function parseBlocks(lines: string[]): Block[] {
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    if (blank(line)) {
      i++;
      continue;
    }
    const fence = FENCE.exec(line);
    if (fence) {
      const marker = fence[1]!;
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i]!.trimStart().startsWith(marker)) body.push(lines[i++]!);
      i++;
      out.push({ t: "code", lang: fence[2] ?? "", text: body.join("\n") });
      continue;
    }
    const h = HEADING.exec(line);
    if (h) {
      out.push({ t: "h", level: h[1]!.length, text: h[2] ?? "" });
      i++;
      continue;
    }
    if (HR.test(line)) {
      out.push({ t: "hr" });
      i++;
      continue;
    }
    if (QUOTE.test(line)) {
      const body: string[] = [];
      while (i < lines.length && !blank(lines[i]!) && (QUOTE.test(lines[i]!) || body.length > 0)) {
        body.push(lines[i]!.replace(QUOTE, ""));
        i++;
      }
      out.push({ t: "quote", blocks: parseBlocks(body) });
      continue;
    }
    const li = LIST.exec(line);
    if (li) {
      const ordered = /\d/.test(li[2]!);
      const start = ordered ? parseInt(li[2]!, 10) : 1;
      const items: { task?: boolean; checked?: boolean; blocks: Block[] }[] = [];
      while (i < lines.length) {
        const m = LIST.exec(lines[i]!);
        if (!m || /\d/.test(m[2]!) !== ordered) break;
        const indent = m[0].length;
        const body = [lines[i]!.slice(indent)];
        i++;
        while (i < lines.length) {
          const l = lines[i]!;
          if (blank(l)) {
            const nextLine = lines[i + 1];
            if (nextLine !== undefined && /^\s+/.test(nextLine) && !blank(nextLine)) {
              body.push("");
              i++;
              continue;
            }
            break;
          }
          const lead = l.length - l.trimStart().length;
          if (lead >= Math.min(indent, 4)) body.push(l.slice(Math.min(lead, indent)));
          else if (LIST.test(l) || startsBlock(l, lines[i + 1])) break;
          else body.push(l);
          i++;
        }
        const task = /^\[([ xX])\]\s+/.exec(body[0]!);
        if (task) body[0] = body[0]!.slice(task[0].length);
        items.push({ task: !!task, checked: task ? task[1] !== " " : undefined, blocks: parseBlocks(body) });
        if (i < lines.length && blank(lines[i]!)) {
          const after = lines[i + 1];
          if (after !== undefined && LIST.test(after)) i++;
        }
      }
      out.push({ t: "list", ordered, start, items });
      continue;
    }
    const next = lines[i + 1];
    if (line.includes("|") && next !== undefined && TABLE_SEP.test(next) && next.includes("-")) {
      const head = splitRow(line);
      const align = splitRow(next).map((c) => {
        const l = c.startsWith(":");
        const r = c.endsWith(":");
        return l && r ? "center" : r ? "right" : l ? "left" : undefined;
      });
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && !blank(lines[i]!) && lines[i]!.includes("|")) rows.push(splitRow(lines[i++]!));
      out.push({ t: "table", align, head, rows });
      continue;
    }
    if (/^( {4}|\t)/.test(line)) {
      const body: string[] = [];
      while (i < lines.length && (/^( {4}|\t)/.test(lines[i]!) || (blank(lines[i]!) && /^( {4}|\t)/.test(lines[i + 1] ?? "")))) {
        body.push(lines[i]!.replace(/^( {4}|\t)/, ""));
        i++;
      }
      out.push({ t: "code", lang: "", text: body.join("\n") });
      continue;
    }
    const para: string[] = [line];
    i++;
    while (i < lines.length && !blank(lines[i]!) && !startsBlock(lines[i]!, lines[i + 1])) {
      if (SETEXT.test(lines[i]!)) break;
      para.push(lines[i]!);
      i++;
    }
    const setext = i < lines.length ? SETEXT.exec(lines[i]!) : null;
    if (setext) {
      out.push({ t: "h", level: setext[1]!.startsWith("=") ? 1 : 2, text: para.join(" ").trim() });
      i++;
      continue;
    }
    out.push({ t: "p", text: para.map((l) => l.trimStart()).join("\n") });
  }
  return out;
}

// ---- Inline ----

const SCHEME = /^([a-z][a-z0-9+.-]*):/i;

/** Returns a safe href, or undefined for schemes other than http(s) and mailto. */
export function safeHref(raw: string, ctx: MarkdownContext): string | undefined {
  // eslint-disable-next-line no-control-regex
  const href = raw.trim().replace(/[\u0000-\u001f\u007f\s]/g, "");
  if (href === "") return undefined;
  const m = SCHEME.exec(href);
  if (m) return ["http", "https", "mailto"].includes(m[1]!.toLowerCase()) ? href : undefined;
  if (href.startsWith("//")) return undefined;
  if (href.startsWith("#")) return `#${anchorId(decodeSafe(href.slice(1)))}`;
  return ctx.resolveLink ? ctx.resolveLink(href) : href;
}

function decodeSafe(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

/** Heading anchor (GitHub style), prefixed so user content cannot clobber app ids. */
export function anchorId(text: string): string {
  return "md-" + text.toLowerCase().trim().replace(/[^\p{L}\p{N}\s_-]/gu, "").replace(/\s/g, "-");
}

function plain(text: string): string {
  return text.replace(/[`*_~[\]]|\(.*?\)|!/g, "");
}

const PUNCT = /[!-/:-@[-`{-~]/;
const BARE_URL = /^https?:\/\/[^\s<]*[^\s<.,:;"')\]*_~]/;

function findClose(text: string, from: number, marker: string): number {
  let j = from;
  while (j < text.length) {
    const k = text.indexOf(marker, j);
    if (k < 0) return -1;
    if (text[k - 1] !== "\\" && !/\s/.test(text[k - 1] ?? " ")) return k;
    j = k + 1;
  }
  return -1;
}

/** Parses [label](target "title") starting at text[i] === "["; returns the end index. */
function parseLink(text: string, i: number): { label: string; target: string; end: number } | undefined {
  let depth = 0;
  let j = i;
  for (; j < text.length; j++) {
    const c = text[j];
    if (c === "\\") j++;
    else if (c === "[") depth++;
    else if (c === "]" && --depth === 0) break;
  }
  if (j >= text.length || text[j + 1] !== "(") return undefined;
  let k = j + 2;
  let paren = 1;
  for (; k < text.length; k++) {
    const c = text[k];
    if (c === "\\") k++;
    else if (c === "(") paren++;
    else if (c === ")" && --paren === 0) break;
  }
  if (k >= text.length) return undefined;
  const inside = text.slice(j + 2, k).trim();
  const target = (inside.startsWith("<") ? inside.slice(1, inside.indexOf(">")) : inside.split(/\s+/)[0]) ?? "";
  return { label: text.slice(i + 1, j), target, end: k + 1 };
}

function inline(text: string, ctx: MarkdownContext, kp: string): ReactNode[] {
  const out: ReactNode[] = [];
  let buf = "";
  let n = 0;
  const push = (node: ReactNode) => {
    if (buf) out.push(buf);
    buf = "";
    out.push(<Fragment key={`${kp}.${n++}`}>{node}</Fragment>);
  };
  for (let i = 0; i < text.length; i++) {
    const c = text[i]!;
    const rest = text.slice(i);
    if (c === "\\" && i + 1 < text.length) {
      const nx = text[i + 1]!;
      if (nx === "\n") {
        push(<br />);
        i++;
        continue;
      }
      if (PUNCT.test(nx)) {
        buf += nx;
        i++;
        continue;
      }
    }
    if (c === "\n") {
      if (buf.endsWith("  ")) {
        buf = buf.trimEnd();
        push(<br />);
      } else buf += " ";
      continue;
    }
    if (c === "`") {
      const run = /^`+/.exec(rest)![0];
      const close = text.indexOf(run, i + run.length);
      if (close > 0) {
        let code = text.slice(i + run.length, close).replace(/\n/g, " ");
        if (/^ .* $/.test(code) && code.trim() !== "") code = code.slice(1, -1);
        push(<code>{code}</code>);
        i = close + run.length - 1;
        continue;
      }
      buf += run;
      i += run.length - 1;
      continue;
    }
    if ((c === "!" && text[i + 1] === "[") || c === "[") {
      const img = c === "!";
      const link = parseLink(text, img ? i + 1 : i);
      if (link) {
        if (img) {
          const src = SCHEME.test(link.target) || link.target.startsWith("//") ? undefined : ctx.resolveImage?.(link.target);
          if (src) push(<img src={src} alt={link.label} loading="lazy" />);
          else {
            const href = safeHref(link.target, {});
            push(href ? <a href={href} rel="noopener noreferrer nofollow">{link.label || link.target}</a> : link.label);
          }
        } else {
          const href = safeHref(link.target, ctx);
          const body = inline(link.label, ctx, `${kp}.${n}l`);
          push(href ? <a href={href} rel={SCHEME.test(href) ? "noopener noreferrer nofollow" : undefined}>{body}</a> : body);
        }
        i = link.end - 1;
        continue;
      }
    }
    if (c === "<") {
      const m = /^<((?:https?:\/\/|mailto:)[^>\s]+)>/.exec(rest);
      if (m) {
        push(<a href={m[1]} rel="noopener noreferrer nofollow">{m[1]!.replace(/^mailto:/, "")}</a>);
        i += m[0].length - 1;
        continue;
      }
    }
    if (c === "h" && !/[\p{L}\p{N}]/u.test(text[i - 1] ?? " ")) {
      const m = BARE_URL.exec(rest);
      if (m) {
        push(<a href={m[0]} rel="noopener noreferrer nofollow">{m[0]}</a>);
        i += m[0].length - 1;
        continue;
      }
    }
    if (c === "~" && text[i + 1] === "~") {
      const close = findClose(text, i + 2, "~~");
      if (close > i + 2) {
        push(<del>{inline(text.slice(i + 2, close), ctx, `${kp}.${n}d`)}</del>);
        i = close + 1;
        continue;
      }
    }
    if (c === "*" || c === "_") {
      const intraword = c === "_" && /[\p{L}\p{N}]/u.test(text[i - 1] ?? " ");
      if (!intraword && !/\s/.test(text[i + 1] ?? " ")) {
        const strong = text[i + 1] === c;
        const marker = strong ? c + c : c;
        const from = i + marker.length;
        if (!/\s/.test(text[from] ?? " ")) {
          const close = findClose(text, from + 1, marker);
          const okEnd = close > 0 && (c !== "_" || !/[\p{L}\p{N}]/u.test(text[close + marker.length] ?? " "));
          if (close > 0 && okEnd) {
            const inner = inline(text.slice(from, close), ctx, `${kp}.${n}e`);
            push(strong ? <strong>{inner}</strong> : <em>{inner}</em>);
            i = close + marker.length - 1;
            continue;
          }
        }
      }
    }
    buf += c;
  }
  if (buf) out.push(buf);
  return out;
}

function renderBlocks(blocks: Block[], ctx: MarkdownContext, kp: string): ReactNode[] {
  return blocks.map((b, i) => {
    const k = `${kp}.${i}`;
    switch (b.t) {
      case "h": {
        const Tag = `h${b.level}` as "h1";
        return <Tag key={k} id={anchorId(plain(b.text))}>{inline(b.text, ctx, k)}</Tag>;
      }
      case "p":
        return <p key={k}>{inline(b.text, ctx, k)}</p>;
      case "code":
        return (
          <pre key={k} data-lang={b.lang || undefined}>
            <code>{b.text}</code>
          </pre>
        );
      case "quote":
        return <blockquote key={k}>{renderBlocks(b.blocks, ctx, k)}</blockquote>;
      case "hr":
        return <hr key={k} />;
      case "list": {
        const items = b.items.map((it, j) => {
          // A single paragraph item renders tight, without the <p> wrapper.
          const only = it.blocks.length === 1 && it.blocks[0]!.t === "p" ? it.blocks[0] : undefined;
          const body = only && only.t === "p" ? inline(only.text, ctx, `${k}.${j}`) : renderBlocks(it.blocks, ctx, `${k}.${j}`);
          return (
            <li key={j} className={it.task ? "md-task" : undefined}>
              {it.task && <input type="checkbox" checked={it.checked} disabled readOnly />}
              {body}
            </li>
          );
        });
        return b.ordered ? <ol key={k} start={b.start === 1 ? undefined : b.start}>{items}</ol> : <ul key={k}>{items}</ul>;
      }
      case "table":
        return (
          <div key={k} className="md-table">
            <table>
              <thead>
                <tr>
                  {b.head.map((c, j) => (
                    <th key={j} style={{ textAlign: b.align[j] }}>{inline(c, ctx, `${k}.h${j}`)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {b.rows.map((r, ri) => (
                  <tr key={ri}>
                    {b.head.map((_, j) => (
                      <td key={j} style={{ textAlign: b.align[j] }}>{inline(r[j] ?? "", ctx, `${k}.${ri}.${j}`)}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
    }
  });
}

/** Renders Markdown source as React elements. */
export function Markdown({ source, ctx = {}, className }: { source: string; ctx?: MarkdownContext; className?: string }) {
  const lines = source.replace(/\r\n?/g, "\n").replace(/<!--[\s\S]*?-->/g, "").split("\n");
  return <div className={className ? `md ${className}` : "md"}>{renderBlocks(parseBlocks(lines), ctx, "m")}</div>;
}

/** Joins a relative path to a base directory, resolving "." and "..". */
export function joinPath(dir: string, rel: string): string {
  const parts = rel.startsWith("/") ? [] : dir.split("/").filter(Boolean);
  for (const seg of rel.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") parts.pop();
    else parts.push(seg);
  }
  return parts.join("/");
}
