// A small, safe Markdown renderer (CommonMark subset plus GFM tables, task lists, strikethrough,
// autolinks and raw HTML, as on GitHub). The Markdown is turned into an HTML string, parsed with
// DOMParser into an inert document, then rebuilt as React elements through an allowlist (see
// `sanitize`): unknown tags are unwrapped, dangerous ones dropped with their content, and only
// known attributes with checked values and http(s)/mailto/relative links survive.
import { createElement, type CSSProperties, type ReactNode } from "react";

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
  | { t: "html"; text: string }
  | { t: "hr" };

const FENCE = /^ {0,3}(`{3,}|~{3,})\s*([^`\s]*)/;
const HEADING = /^ {0,3}(#{1,6})(?:\s+(.*?))?\s*#*\s*$/;
const HR = /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/;
const QUOTE = /^ {0,3}> ?/;
const LIST = /^( {0,3})([-*+]|\d{1,9}[.)])(\s+|$)/;
const TABLE_SEP = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;
const SETEXT = /^ {0,3}(=+|-+)\s*$/;

// ---- Raw HTML (CommonMark §4.6 and §6.6) ----

const ATTR = String.raw`\s+[a-zA-Z_:][a-zA-Z0-9_.:-]*(?:\s*=\s*(?:[^\s"'=<>\x60]+|'[^']*'|"[^"]*"))?`;
const OPEN_TAG = new RegExp(String.raw`^<[a-zA-Z][a-zA-Z0-9-]*(?:${ATTR})*\s*\/?>`);
const CLOSE_TAG = /^<\/[a-zA-Z][a-zA-Z0-9-]*\s*>/;
const COMMENT = /^<!--[\s\S]*?-->/;
const RAW_START = /^ {0,3}<(script|pre|style|textarea)(?=[\s>]|$)/i;
const RAW_END = /<\/(script|pre|style|textarea)>/i;
const TAG_START = /^ {0,3}<\/?([a-zA-Z][a-zA-Z0-9-]*)(?=[\s/>]|$)/;
const BLOCK_TAGS = new Set(
  ("address article aside base basefont blockquote body caption center col colgroup dd details dialog dir div dl dt " +
    "fieldset figcaption figure footer form frame frameset h1 h2 h3 h4 h5 h6 head header hr html iframe legend li link " +
    "main menu menuitem nav noframes ol optgroup option p param picture search section source summary table tbody td " +
    "tfoot th thead title tr track ul").split(" "),
);

/** CommonMark HTML block start: 1 raw (script, pre…), 2 comment, 6 block tag, 7 lone tag; 0 if none. */
function htmlBlockKind(line: string): 0 | 1 | 2 | 6 | 7 {
  if (RAW_START.test(line)) return 1;
  if (/^ {0,3}<!--/.test(line)) return 2;
  const m = TAG_START.exec(line);
  if (!m) return 0;
  if (BLOCK_TAGS.has(m[1]!.toLowerCase())) return 6;
  const s = line.trim();
  const tag = OPEN_TAG.exec(s) ?? CLOSE_TAG.exec(s);
  return tag && tag[0].length === s.length ? 7 : 0;
}

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
  const html = htmlBlockKind(line);
  return (
    FENCE.test(line) || HEADING.test(line) || HR.test(line) || QUOTE.test(line) || LIST.test(line) ||
    (html !== 0 && html !== 7) ||
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
    const html = htmlBlockKind(line);
    if (html !== 0) {
      const body: string[] = [];
      if (html === 1 || html === 2) {
        const end = html === 1 ? RAW_END : /-->/;
        while (i < lines.length) {
          const l = lines[i++]!;
          body.push(l);
          if (end.test(l)) break;
        }
      } else {
        while (i < lines.length && !blank(lines[i]!)) body.push(lines[i++]!);
      }
      out.push({ t: "html", text: body.join("\n") });
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

// ---- Inline (to an HTML string) ----

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

/** Escapes text for HTML, keeping valid entity references (&amp;, &#169;) for the parser to decode. */
function esc(s: string): string {
  return s
    .replace(/&(?!(?:#[0-9]{1,7}|#[xX][0-9a-fA-F]{1,6}|[a-zA-Z][a-zA-Z0-9]{1,31});)/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

/** Escapes everything, entities included (code spans and blocks show them as written). */
function escAll(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
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

function inline(text: string): string {
  let out = "";
  let buf = "";
  const push = (html: string) => {
    out += esc(buf) + html;
    buf = "";
  };
  for (let i = 0; i < text.length; i++) {
    const c = text[i]!;
    const rest = text.slice(i);
    if (c === "\\" && i + 1 < text.length) {
      const nx = text[i + 1]!;
      if (nx === "\n") {
        push("<br>");
        i++;
        continue;
      }
      if (PUNCT.test(nx)) {
        push(escAll(nx));
        i++;
        continue;
      }
    }
    if (c === "\n") {
      if (buf.endsWith("  ")) {
        buf = buf.trimEnd();
        push("<br>");
      } else buf += "\n";
      continue;
    }
    if (c === "`") {
      const run = /^`+/.exec(rest)![0];
      const close = text.indexOf(run, i + run.length);
      if (close > 0) {
        let code = text.slice(i + run.length, close).replace(/\n/g, " ");
        if (/^ .* $/.test(code) && code.trim() !== "") code = code.slice(1, -1);
        push(`<code>${escAll(code)}</code>`);
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
        if (img) push(`<img src="${esc(link.target)}" alt="${esc(plain(link.label))}">`);
        else push(`<a href="${esc(link.target)}">${inline(link.label)}</a>`);
        i = link.end - 1;
        continue;
      }
    }
    if (c === "<") {
      const auto = /^<((?:https?:\/\/|mailto:)[^>\s]+)>/.exec(rest);
      if (auto) {
        push(`<a href="${esc(auto[1]!)}">${esc(auto[1]!.replace(/^mailto:/, ""))}</a>`);
        i += auto[0].length - 1;
        continue;
      }
      const tag = COMMENT.exec(rest) ?? OPEN_TAG.exec(rest) ?? CLOSE_TAG.exec(rest);
      if (tag) {
        push(tag[0]);
        i += tag[0].length - 1;
        continue;
      }
    }
    if (c === "h" && !/[\p{L}\p{N}]/u.test(text[i - 1] ?? " ")) {
      const m = BARE_URL.exec(rest);
      if (m) {
        push(`<a href="${esc(m[0])}">${esc(m[0])}</a>`);
        i += m[0].length - 1;
        continue;
      }
    }
    if (c === "~" && text[i + 1] === "~") {
      const close = findClose(text, i + 2, "~~");
      if (close > i + 2) {
        push(`<del>${inline(text.slice(i + 2, close))}</del>`);
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
            const inner = inline(text.slice(from, close));
            push(strong ? `<strong>${inner}</strong>` : `<em>${inner}</em>`);
            i = close + marker.length - 1;
            continue;
          }
        }
      }
    }
    buf += c;
  }
  return out + esc(buf);
}

function renderBlocks(blocks: Block[]): string {
  return blocks
    .map((b) => {
      switch (b.t) {
        case "h":
          return `<h${b.level}>${inline(b.text)}</h${b.level}>`;
        case "p":
          return `<p>${inline(b.text)}</p>`;
        case "code":
          return `<pre${b.lang ? ` data-lang="${escAll(b.lang)}"` : ""}><code>${escAll(b.text)}</code></pre>`;
        case "quote":
          return `<blockquote>${renderBlocks(b.blocks)}</blockquote>`;
        case "hr":
          return "<hr>";
        case "html":
          return b.text;
        case "list": {
          const items = b.items.map((it) => {
            // A single paragraph item renders tight, without the <p> wrapper.
            const only = it.blocks.length === 1 && it.blocks[0]!.t === "p" ? it.blocks[0] : undefined;
            const body = only && only.t === "p" ? inline(only.text) : renderBlocks(it.blocks);
            const box = it.task ? `<input type="checkbox"${it.checked ? " checked" : ""} disabled>` : "";
            return `<li>${box}${body}</li>`;
          });
          return b.ordered ? `<ol${b.start === 1 ? "" : ` start="${b.start}"`}>${items.join("")}</ol>` : `<ul>${items.join("")}</ul>`;
        }
        case "table": {
          const al = (j: number) => (b.align[j] ? ` align="${b.align[j]}"` : "");
          const head = b.head.map((c, j) => `<th${al(j)}>${inline(c)}</th>`).join("");
          const rows = b.rows.map((r) => `<tr>${b.head.map((_, j) => `<td${al(j)}>${inline(r[j] ?? "")}</td>`).join("")}</tr>`).join("");
          return `<table><thead><tr>${head}</tr></thead><tbody>${rows}</tbody></table>`;
        }
      }
    })
    .join("\n");
}

// ---- Sanitizer: inert DOM → React elements ----

/** Dropped together with everything inside them. */
const DROP = new Set(
  ("script style iframe frame frameset object embed applet template noscript svg math textarea select option button " +
    "title head link meta base audio video canvas form dialog param track noembed noframes xmp plaintext portal").split(" "),
);

/** Kept (GitHub's allowlist); obsolete names are mapped to their modern element. Others are unwrapped. */
const TAGS: Record<string, string> = Object.fromEntries(
  ("a abbr b bdi bdo blockquote br caption cite code dd del details dfn div dl dt em figcaption figure h1 h2 h3 h4 h5 h6 " +
    "hr i img ins kbd li mark ol p picture pre q rp rt ruby s samp small source span strong sub summary sup table tbody " +
    "td tfoot th thead time tr u ul var wbr").split(" ").map((t) => [t, t]),
);
TAGS.strike = "s";
TAGS.tt = "code";
TAGS.center = "div";

const VOID = new Set(["br", "hr", "img", "source", "wbr", "input"]);
/** Elements whose whitespace-only text children are not valid DOM for React. */
const NO_TEXT = new Set(["table", "thead", "tbody", "tfoot", "tr", "picture"]);
const TEXT_ALIGN = new Set(["p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "td", "th", "caption", "summary", "dt", "dd", "li", "blockquote"]);
const MAX_DEPTH = 128;

const SIZE = /^\d{1,5}(\.\d{1,3})?(%|px)?$/;
const INT = /^\d{1,4}$/;

function imageSource(raw: string, ctx: MarkdownContext): { src?: string; link?: string } {
  // eslint-disable-next-line no-control-regex
  const s = raw.trim().replace(/[\u0000-\u001f\u007f]/g, "");
  if (s === "") return {};
  if (/^https:\/\//i.test(s)) return { src: s };
  if (SCHEME.test(s) || s.startsWith("//")) return { link: safeHref(s, {}) };
  const src = ctx.resolveImage?.(s);
  return src ? { src } : { link: safeHref(s, ctx) };
}

function srcset(raw: string, ctx: MarkdownContext): string | undefined {
  const parts: string[] = [];
  for (const cand of raw.split(",")) {
    const [url = "", ...desc] = cand.trim().split(/\s+/);
    const d = desc.join(" ");
    const src = imageSource(url, ctx).src;
    if (!src || !/^[\d.]*[wx]?$/.test(d)) continue;
    parts.push(d ? `${src} ${d}` : src);
  }
  return parts.length > 0 ? parts.join(", ") : undefined;
}

function alignStyle(tag: string, align: string | null, valign: string | null): CSSProperties | undefined {
  const st: CSSProperties = {};
  const a = align?.toLowerCase();
  if (tag === "img") {
    if (a === "left" || a === "right") Object.assign(st, { float: a, margin: a === "left" ? "0 1em .5em 0" : "0 0 .5em 1em" });
    else if (a === "middle" || a === "top" || a === "bottom") st.verticalAlign = a;
  } else if (tag === "table") {
    if (a === "center") Object.assign(st, { marginLeft: "auto", marginRight: "auto" });
    else if (a === "right") st.marginLeft = "auto";
  } else if (a && TEXT_ALIGN.has(tag) && ["left", "right", "center", "justify"].includes(a)) {
    st.textAlign = a as CSSProperties["textAlign"];
  }
  const v = valign?.toLowerCase();
  if (v && (tag === "td" || tag === "th" || tag === "tr") && ["top", "middle", "bottom", "baseline"].includes(v)) {
    st.verticalAlign = v as CSSProperties["verticalAlign"];
  }
  return Object.keys(st).length > 0 ? st : undefined;
}

function children(node: Node, ctx: MarkdownContext, depth: number, parentTag: string, kp: string): ReactNode[] {
  const out: ReactNode[] = [];
  node.childNodes.forEach((n, i) => {
    if (n.nodeType === Node.TEXT_NODE) {
      const text = n.textContent ?? "";
      if (!(NO_TEXT.has(parentTag) && text.trim() === "")) out.push(text);
    } else if (n.nodeType === Node.ELEMENT_NODE) {
      const r = element(n as Element, ctx, depth + 1, `${kp}.${i}`);
      if (Array.isArray(r)) out.push(...r);
      else if (r !== null) out.push(r);
    }
  });
  return out;
}

function element(el: Element, ctx: MarkdownContext, depth: number, key: string): ReactNode | ReactNode[] {
  const src = el.localName;
  if (depth > MAX_DEPTH || DROP.has(src)) return null;
  const tag = TAGS[src];
  if (tag === undefined) {
    // Unknown or unsupported tag: keep only what is inside. The task checkbox is the one input kept.
    if (src === "input") return taskBox(el, key);
    return children(el, ctx, depth, src, key);
  }
  const get = (name: string) => el.getAttribute(name);
  const props: Record<string, unknown> = { key };

  for (const name of ["title", "lang", "dir"]) {
    const v = get(name);
    if (v === null) continue;
    if (name === "lang" && !/^[a-zA-Z-]{1,35}$/.test(v)) continue;
    if (name === "dir" && !["ltr", "rtl", "auto"].includes(v)) continue;
    props[name] = v;
  }
  const id = get("id") ?? (src === "a" ? get("name") : null);
  if (id) props.id = anchorId(id);
  for (const name of ["width", "height"]) {
    const v = get(name)?.trim();
    if (v && SIZE.test(v) && (tag === "img" || tag === "source" || tag === "table" || tag === "td" || tag === "th")) props[name] = v;
  }
  const style = alignStyle(tag, src === "center" ? "center" : get("align"), get("valign"));
  if (style) props.style = style;

  switch (tag) {
    case "h1":
    case "h2":
    case "h3":
    case "h4":
    case "h5":
    case "h6":
      props.id = anchorId(plain(el.textContent ?? ""));
      break;
    case "a": {
      const raw = get("href");
      const href = raw === null ? undefined : safeHref(raw, ctx);
      if (href) {
        props.href = href;
        if (SCHEME.test(href)) props.rel = "noopener noreferrer nofollow";
      }
      break;
    }
    case "img": {
      const s = imageSource(get("src") ?? "", ctx);
      const alt = get("alt") ?? "";
      if (!s.src) {
        if (s.link) return createElement("a", { key, href: s.link, rel: SCHEME.test(s.link) ? "noopener noreferrer nofollow" : undefined }, alt || s.link);
        return alt || null;
      }
      Object.assign(props, { src: s.src, alt, loading: "lazy", decoding: "async", referrerPolicy: "no-referrer" });
      break;
    }
    case "source": {
      const set = srcset(get("srcset") ?? "", ctx);
      if (!set || el.parentElement?.localName !== "picture") return null;
      props.srcSet = set;
      const media = get("media");
      if (media && /^[\w\s():,.-]{1,200}$/.test(media)) props.media = media;
      const type = get("type");
      if (type && /^[\w.+-]+\/[\w.+-]+$/.test(type)) props.type = type;
      break;
    }
    case "ol": {
      const start = get("start");
      if (start && INT.test(start)) props.start = Number(start);
      if (el.hasAttribute("reversed")) props.reversed = true;
      const type = get("type");
      if (type && /^[1aAiI]$/.test(type)) props.type = type;
      break;
    }
    case "li": {
      const value = get("value");
      if (value && INT.test(value)) props.value = Number(value);
      const first = Array.from(el.childNodes).find((c) => !(c.nodeType === Node.TEXT_NODE && (c.textContent ?? "").trim() === ""));
      if (first instanceof Element && first.localName === "input" && first.getAttribute("type") === "checkbox") props.className = "md-task";
      break;
    }
    case "td":
    case "th":
      for (const [name, prop] of [["colspan", "colSpan"], ["rowspan", "rowSpan"]] as const) {
        const v = get(name);
        if (v && INT.test(v) && Number(v) > 0) props[prop] = Math.min(Number(v), 1000);
      }
      if (tag === "th") {
        const scope = get("scope");
        if (scope && ["row", "col", "rowgroup", "colgroup"].includes(scope)) props.scope = scope;
      }
      break;
    case "details":
      if (el.hasAttribute("open")) props.open = true;
      break;
    case "time":
    case "del":
    case "ins": {
      const dt = get("datetime");
      if (dt && /^[\d\-:.TZ+ ]{1,40}$/.test(dt)) props.dateTime = dt;
      break;
    }
    case "pre": {
      const lang = get("data-lang");
      if (lang && /^[\w+#.-]{1,40}$/.test(lang)) props["data-lang"] = lang;
      break;
    }
  }

  if (VOID.has(tag)) return createElement(tag, props);
  const node = createElement(tag, props, ...children(el, ctx, depth, tag, key));
  // Tables scroll inside their own box instead of widening the page.
  return tag === "table" ? createElement("div", { key, className: "md-table" }, node) : node;
}

function taskBox(el: Element, key: string): ReactNode {
  const li = el.parentElement;
  if (el.getAttribute("type") !== "checkbox" || li?.localName !== "li") return null;
  return createElement("input", { key, type: "checkbox", checked: el.hasAttribute("checked"), disabled: true, readOnly: true });
}

/** Renders Markdown source (with GitHub-style raw HTML) as React elements. */
export function Markdown({ source, ctx = {}, className }: { source: string; ctx?: MarkdownContext; className?: string }) {
  const html = renderBlocks(parseBlocks(source.replace(/\r\n?/g, "\n").split("\n")));
  // A DOMParser document has no browsing context: nothing in it runs or loads.
  const doc = new DOMParser().parseFromString(`<!doctype html><html><body>${html}</body></html>`, "text/html");
  return <div className={className ? `md ${className}` : "md"}>{children(doc.body, ctx, 0, "div", "m")}</div>;
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
