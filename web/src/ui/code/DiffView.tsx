import { useMemo, useState, useSyncExternalStore, type CSSProperties, type ReactNode } from "react";
import type { Diff, DiffLine, FileDiff } from "../../api/code.ts";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../Icon.tsx";
import { columns, ROW } from "./CodeView.tsx";
import { useVirtualRows } from "./virtual.ts";

/** Changed span of a line [start, end) for the word-level highlight. */
type Span = [number, number];

type Row =
  | { k: "hunk"; text: string }
  | { k: "line"; line: DiffLine; x?: Span }
  | { k: "pair"; l?: DiffLine; r?: DiffLine; lx?: Span; rx?: Span };

export type DiffMode = "unified" | "split";

const MODE_KEY = "erepo.diffMode";
const NARROW = "(max-width: 900px)";

function readMode(): DiffMode {
  try {
    return localStorage.getItem(MODE_KEY) === "unified" ? "unified" : "split";
  } catch {
    return "split";
  }
}

function useNarrow(): boolean {
  return useSyncExternalStore(
    (fn) => {
      const mq = window.matchMedia(NARROW);
      mq.addEventListener("change", fn);
      return () => mq.removeEventListener("change", fn);
    },
    () => window.matchMedia(NARROW).matches,
  );
}

/**
 * Side by side is the default (round 020) on wide screens; below 900 px only unified
 * fits. The choice is remembered per browser.
 */
export function useDiffMode(): [DiffMode, (m: DiffMode) => void, boolean] {
  const narrow = useNarrow();
  const [mode, setMode] = useState<DiffMode>(readMode);
  const set = (m: DiffMode) => {
    setMode(m);
    try {
      localStorage.setItem(MODE_KEY, m);
    } catch {
      // storage unavailable: keep the choice for this page only
    }
  };
  return [narrow ? "unified" : mode, set, narrow];
}

/** Changed middle of two similar lines: common prefix and suffix are left plain. */
function intra(a: string, b: string): [Span, Span] | undefined {
  const max = Math.min(a.length, b.length);
  let p = 0;
  while (p < max && a[p] === b[p]) p++;
  let s = 0;
  while (s < max - p && a[a.length - 1 - s] === b[b.length - 1 - s]) s++;
  if (p + s === 0) return undefined;
  return [
    [p, a.length - s],
    [p, b.length - s],
  ];
}

/** Walks a hunk pairing each run of deletions with the additions that follow it. */
function runs(lines: DiffLine[], onPair: (dels: DiffLine[], adds: DiffLine[]) => void, onOther: (l: DiffLine, i: number) => void) {
  let dels: DiffLine[] = [];
  let adds: DiffLine[] = [];
  const flush = () => {
    if (dels.length || adds.length) onPair(dels, adds);
    dels = [];
    adds = [];
  };
  lines.forEach((line, i) => {
    if (line.type === "delete") {
      if (adds.length) flush();
      dels.push(line);
    } else if (line.type === "add") adds.push(line);
    else {
      flush();
      onOther(line, i);
    }
  });
  flush();
}

function unifiedRows(f: FileDiff): Row[] {
  const rows: Row[] = [];
  for (const h of f.hunks) {
    rows.push({ k: "hunk", text: h.header });
    runs(
      h.lines,
      (dels, adds) => {
        const spans = dels.map((d, i) => (adds[i] ? intra(d.text, adds[i].text) : undefined));
        dels.forEach((line, i) => rows.push({ k: "line", line, x: spans[i]?.[0] }));
        adds.forEach((line, i) => rows.push({ k: "line", line, x: spans[i]?.[1] }));
      },
      (line) => rows.push({ k: "line", line }),
    );
  }
  return rows;
}

function splitRows(f: FileDiff): Row[] {
  const rows: Row[] = [];
  for (const h of f.hunks) {
    rows.push({ k: "hunk", text: h.header });
    runs(
      h.lines,
      (dels, adds) => {
        for (let i = 0; i < Math.max(dels.length, adds.length); i++) {
          const l = dels[i];
          const r = adds[i];
          const sp = l && r ? intra(l.text, r.text) : undefined;
          rows.push({ k: "pair", l, r, lx: sp?.[0], rx: sp?.[1] });
        }
      },
      (line, i) => {
        if (line.type === "context") rows.push({ k: "pair", l: line, r: line });
        else {
          // "no newline" marker: show it on the side of the line it follows.
          const prev = h.lines[i - 1];
          rows.push(prev?.type === "add" ? { k: "pair", r: line } : prev?.type === "delete" ? { k: "pair", l: line } : { k: "pair", l: line, r: line });
        }
      },
    );
  }
  return rows;
}

const cls = (l: DiffLine | undefined) =>
  !l ? "dv-none" : l.type === "add" ? "dv-add" : l.type === "delete" ? "dv-del" : l.type === "no-newline" ? "dv-nn" : "";
const mark = (l: DiffLine) => (l.type === "add" ? "+" : l.type === "delete" ? "−" : "");
const num = (n: number) => (n > 0 ? n : "");

function Text({ text, x }: { text: string; x?: Span }) {
  if (!x || x[1] <= x[0]) return <>{text}</>;
  return (
    <>
      {text.slice(0, x[0])}
      <span className="dv-x">{text.slice(x[0], x[1])}</span>
      {text.slice(x[1])}
    </>
  );
}

function Side({ line, x }: { line?: DiffLine; x?: Span }) {
  const c = cls(line);
  const n = line ? (line.type === "add" ? line.newLine : line.oldLine || line.newLine) : 0;
  return (
    <>
      <span className={`dv-ln ${c}`}>{line && line.type !== "no-newline" ? num(n) : ""}</span>
      <span className={`dv-mk ${c}`}>{line ? mark(line) : ""}</span>
      <span className={`dv-tx ${c}`}>{line ? <Text text={line.text} x={x} /> : ""}</span>
    </>
  );
}

function FileBody({ file, mode }: { file: FileDiff; mode: DiffMode }) {
  const t = useT("code");
  const rows = useMemo(() => (mode === "split" ? splitRows(file) : unifiedRows(file)), [file, mode]);
  const { ref, start, end, height } = useVirtualRows(rows.length, ROW);
  const geo = useMemo(() => {
    let maxCols = 0;
    let maxNum = 0;
    for (const h of file.hunks) {
      maxNum = Math.max(maxNum, h.oldStart + h.oldLines, h.newStart + h.newLines);
      for (const l of h.lines) maxCols = Math.max(maxCols, columns(l.text));
    }
    return { cols: maxCols + 2, digits: String(maxNum).length + 2 };
  }, [file]);

  if (file.binary) return <p className="ui-note acc-pad">{t("diff.binary")}</p>;
  if (rows.length === 0) return <p className="ui-note acc-pad">{file.truncated ? t("diff.fileTruncated") : t("diff.noChanges")}</p>;

  const style = { "--ln-w": `${geo.digits}ch`, "--dv-cols": `${geo.cols}ch`, height } as CSSProperties;
  const out: ReactNode[] = [];
  for (let i = start; i < end; i++) {
    const r = rows[i]!;
    if (r.k === "hunk") out.push(<div key={i} className="dv-row dv-hunk"><span className="dv-tx">{r.text}</span></div>);
    else if (r.k === "line") {
      const l = r.line;
      out.push(
        <div key={i} className={`dv-row ${cls(l)}`}>
          <span className="dv-ln">{l.type === "add" || l.type === "no-newline" ? "" : num(l.oldLine)}</span>
          <span className="dv-ln">{l.type === "delete" || l.type === "no-newline" ? "" : num(l.newLine)}</span>
          <span className="dv-mk">{mark(l)}</span>
          <span className="dv-tx">
            <Text text={l.text} x={r.x} />
          </span>
        </div>,
      );
    } else {
      out.push(
        <div key={i} className="dv-row dv-pair">
          <Side line={r.l} x={r.lx} />
          <Side line={r.r} x={r.rx} />
        </div>,
      );
    }
  }
  return (
    <div className={mode === "split" ? "dv dv-split" : "dv"}>
      <div ref={ref} className="dv-body" style={style}>
        <div className="dv-win" style={{ transform: `translateY(${start * ROW}px)` }}>
          {out}
        </div>
      </div>
      {file.truncated && <p className="ui-note acc-pad">{t("diff.fileTruncated")}</p>}
    </div>
  );
}

/** Body of one file's diff in the remembered layout (the editor's "Preview changes"). */
export function DiffBody({ file }: { file: FileDiff }) {
  const [mode] = useDiffMode();
  return <FileBody file={file} mode={mode} />;
}

export const filePath = (f: FileDiff) => (f.status === "deleted" ? f.oldPath : f.newPath);

const statusIcon: Record<FileDiff["status"], string> = {
  added: "ui-st-open",
  modified: "ui-st-warn",
  deleted: "ui-st-fail",
  renamed: "ui-st-info",
  copied: "ui-st-info",
};

/** Five squares, GitHub style: the share of additions and deletions in the file. */
function Blocks({ add, del }: { add: number; del: number }) {
  const total = add + del;
  const a = total === 0 ? 0 : Math.round((add / total) * 5);
  const d = total === 0 ? 0 : Math.min(5 - a, Math.round((del / total) * 5));
  return (
    <span className="dv-blocks" aria-hidden="true">
      {Array.from({ length: 5 }, (_, i) => (
        <i key={i} className={i < a ? "a" : i < a + d ? "d" : undefined} />
      ))}
    </span>
  );
}

function FileCard({ file, index, mode, actions }: { file: FileDiff; index: number; mode: DiffMode; actions?: ReactNode }) {
  const t = useT("code");
  const [open, setOpen] = useState(file.status !== "deleted");
  const renamed = (file.status === "renamed" || file.status === "copied") && file.oldPath !== file.newPath;
  return (
    <section className="dv-file" id={`diff-${index}`} aria-label={filePath(file)}>
      <div className="dv-fh">
        <button type="button" className="ui-iconbtn" aria-expanded={open} aria-label={open ? t("diff.collapse") : t("diff.expand")} onClick={() => setOpen(!open)}>
          <Icon name={open ? "down" : "right"} />
        </button>
        <span className="dv-path">{renamed ? `${file.oldPath} → ${file.newPath}` : filePath(file)}</span>
        {file.status !== "modified" && <span className="ui-chip">{t(`diff.status.${file.status}`)}</span>}
        <span className="dv-fr">
          <span className="dv-p">+{file.additions}</span>
          <span className="dv-m">−{file.deletions}</span>
          <Blocks add={file.additions} del={file.deletions} />
          {actions}
        </span>
      </div>
      {open && <FileBody file={file} mode={mode} />}
    </section>
  );
}

/** Files of a diff grouped by directory, in diff order. */
function groups(files: FileDiff[], filter: string) {
  const q = filter.trim().toLowerCase();
  const m = new Map<string, { file: FileDiff; index: number }[]>();
  files.forEach((file, index) => {
    const p = filePath(file);
    if (q && !p.toLowerCase().includes(q)) return;
    const dir = p.includes("/") ? p.slice(0, p.lastIndexOf("/") + 1) : "/";
    const g = m.get(dir);
    if (g) g.push({ file, index });
    else m.set(dir, [{ file, index }]);
  });
  return [...m.entries()];
}

/**
 * Diff layout of the commit detail, the compare page and the PR "Files changed" tab
 * (round 020): a full-height file column (filter, grouped by folder, +/−) beside the
 * main column, which shows `header`, a bar (summary, `toolbar`, Unified / Split) and one
 * card per file. `fileActions` adds controls to each file header.
 */
export function DiffView({ diff, fileActions, header, toolbar }: {
  diff: Diff;
  fileActions?: (f: FileDiff) => ReactNode;
  header?: ReactNode;
  toolbar?: ReactNode;
}) {
  const t = useT("code");
  const [mode, setMode, narrow] = useDiffMode();
  const [filter, setFilter] = useState("");
  const [active, setActive] = useState(-1);
  const grouped = useMemo(() => groups(diff.files, filter), [diff.files, filter]);
  const jump = (i: number) => {
    setActive(i);
    document.getElementById(`diff-${i}`)?.scrollIntoView({ block: "start" });
  };
  return (
    <div className="dv-page">
      {diff.files.length > 0 && (
        <nav className="dv-files" aria-label={t("diff.files", { count: diff.files.length })}>
          <label className="cx-find">
            <Icon name="search" />
            <input type="search" value={filter} placeholder={t("diff.filter")} aria-label={t("diff.filter")} onChange={(e) => setFilter(e.target.value)} />
          </label>
          {grouped.map(([dir, fs]) => (
            <div key={dir} className="dv-fg">
              <div className="dv-dir">{dir}</div>
              {fs.map(({ file, index }) => (
                <a
                  key={index}
                  href={`#diff-${index}`}
                  aria-current={active === index ? "location" : undefined}
                  title={filePath(file)}
                  onClick={(e) => {
                    e.preventDefault();
                    jump(index);
                  }}
                >
                  <Icon name="file" className={`ui-icon ${statusIcon[file.status]}`} />
                  <span>{filePath(file).split("/").pop()}</span>
                  <span className="dv-n">
                    {file.additions > 0 && <span className="dv-p">+{file.additions}</span>}
                    {file.deletions > 0 && <span className="dv-m">−{file.deletions}</span>}
                  </span>
                </a>
              ))}
            </div>
          ))}
        </nav>
      )}
      <div className="dv-main">
        {header}
        <div className="dv-bar">
          <span className="dv-sum">
            {t("diff.summary", { files: diff.files.length })} <span className="dv-p">+{diff.additions}</span> <span className="dv-m">−{diff.deletions}</span>
          </span>
          <span className="acc-grow" />
          {toolbar}
          {!narrow && diff.files.length > 0 && (
            <div className="ui-seg" role="group" aria-label={t("diff.mode")}>
              {(["unified", "split"] as const).map((m) => (
                <button key={m} type="button" className="ui-segi" aria-pressed={mode === m} onClick={() => setMode(m)}>
                  {t(`diff.mode.${m}`)}
                </button>
              ))}
            </div>
          )}
        </div>
        {diff.files.length === 0 && <p className="ui-note acc-pad">{t("diff.empty")}</p>}
        {diff.truncated && <p className="ui-note acc-pad" role="status">{t("diff.truncated")}</p>}
        {diff.files.map((f, i) => (
          <FileCard key={`${i}-${mode}`} file={f} index={i} mode={mode} actions={fileActions?.(f)} />
        ))}
      </div>
    </div>
  );
}
