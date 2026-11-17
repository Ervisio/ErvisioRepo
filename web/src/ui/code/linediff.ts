import type { DiffLine, FileDiff } from "../../api/code.ts";

const CONTEXT = 3;
// Above this many cells the middle is shown as a full replacement instead of an LCS.
const MAX_CELLS = 4_000_000;

type Op = { type: "context" | "add" | "delete"; text: string; oldLine: number; newLine: number };

/** Line operations turning `a` into `b`: common prefix/suffix, LCS in between. */
function ops(a: string[], b: string[]): Op[] {
  let p = 0;
  while (p < a.length && p < b.length && a[p] === b[p]) p++;
  let s = 0;
  while (s < a.length - p && s < b.length - p && a[a.length - 1 - s] === b[b.length - 1 - s]) s++;
  const am = a.slice(p, a.length - s);
  const bm = b.slice(p, b.length - s);
  const out: Op[] = [];
  for (let i = 0; i < p; i++) out.push({ type: "context", text: a[i]!, oldLine: i + 1, newLine: i + 1 });
  let o = p;
  let n = p;
  const del = (text: string) => out.push({ type: "delete", text, oldLine: ++o, newLine: 0 });
  const add = (text: string) => out.push({ type: "add", text, oldLine: 0, newLine: ++n });
  const keep = (text: string) => out.push({ type: "context", text, oldLine: ++o, newLine: ++n });
  if (am.length * bm.length > MAX_CELLS) {
    am.forEach(del);
    bm.forEach(add);
  } else {
    const w = bm.length + 1;
    const lcs = new Uint32Array((am.length + 1) * w);
    for (let i = am.length - 1; i >= 0; i--)
      for (let j = bm.length - 1; j >= 0; j--)
        lcs[i * w + j] = am[i] === bm[j] ? lcs[(i + 1) * w + j + 1]! + 1 : Math.max(lcs[(i + 1) * w + j]!, lcs[i * w + j + 1]!);
    let i = 0;
    let j = 0;
    while (i < am.length && j < bm.length) {
      if (am[i] === bm[j]) {
        keep(am[i]!);
        i++;
        j++;
      } else if (lcs[(i + 1) * w + j]! >= lcs[i * w + j + 1]!) del(am[i++]!);
      else add(bm[j++]!);
    }
    while (i < am.length) del(am[i++]!);
    while (j < bm.length) add(bm[j++]!);
  }
  for (let i = a.length - s; i < a.length; i++) keep(a[i]!);
  return out;
}

/** A FileDiff (hunks with three lines of context) between two versions of a text file. */
export function lineDiff(path: string, before: string[], after: string[], created: boolean): FileDiff {
  const all = ops(before, after);
  const hunks: FileDiff["hunks"] = [];
  let additions = 0;
  let deletions = 0;
  let i = 0;
  while (i < all.length) {
    if (all[i]!.type === "context") {
      i++;
      continue;
    }
    // Grow the hunk while the next change is within 2 × CONTEXT lines.
    const from = Math.max(0, i - CONTEXT);
    let end = i;
    for (let k = i; k < all.length; k++) {
      if (all[k]!.type !== "context") end = k;
      else if (k - end > CONTEXT * 2) break;
    }
    const to = Math.min(all.length, end + CONTEXT + 1);
    const lines: DiffLine[] = all.slice(from, to).map((x) => ({ type: x.type, text: x.text, oldLine: x.oldLine, newLine: x.newLine }));
    for (const l of lines) {
      if (l.type === "add") additions++;
      else if (l.type === "delete") deletions++;
    }
    const first = all[from]!;
    const oldStart = first.oldLine || (all.slice(from, to).find((x) => x.oldLine)?.oldLine ?? 0);
    const newStart = first.newLine || (all.slice(from, to).find((x) => x.newLine)?.newLine ?? 0);
    const oldLines = lines.filter((l) => l.type !== "add").length;
    const newLines = lines.filter((l) => l.type !== "delete").length;
    hunks.push({ header: `@@ -${oldStart},${oldLines} +${newStart},${newLines} @@`, oldStart, oldLines, newStart, newLines, lines });
    i = to;
  }
  return {
    status: created ? "added" : "modified",
    oldPath: path,
    newPath: path,
    oldMode: "100644",
    newMode: "100644",
    oldOid: "",
    newOid: "",
    similarity: 100,
    binary: false,
    additions,
    deletions,
    hunks,
    truncated: false,
  };
}
