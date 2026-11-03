import { useMemo, useState, useSyncExternalStore, type CSSProperties, type ReactNode } from "react";
import type { Diff, DiffLine, FileDiff } from "../../api/code.ts";
import { useT } from "../../i18n/useT.ts";
import { Card } from "../Card.tsx";
import { Icon } from "../Icon.tsx";
import { columns, ROW } from "./CodeView.tsx";
import { useVirtualRows } from "./virtual.ts";

type Row =
  | { k: "hunk"; text: string }
  | { k: "line"; line: DiffLine }
  | { k: "pair"; l?: DiffLine; r?: DiffLine };

export type DiffMode = "unified" | "split";

const MODE_KEY = "erepo.diffMode";
const NARROW = "(max-width: 900px)";

function readMode(): DiffMode {
  try {
    return localStorage.getItem(MODE_KEY) === "split" ? "split" : "unified";
  } catch {
    return "unified";
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
 * Unified is the default (it is what the approved mock shows) and the only mode below
 * 900 px; on wider screens the user can switch to side by side, remembered per browser.
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

function unifiedRows(f: FileDiff): Row[] {
  const rows: Row[] = [];
  for (const h of f.hunks) {
    rows.push({ k: "hunk", text: h.header });
    for (const line of h.lines) rows.push({ k: "line", line });
  }
  return rows;
}

/** Pairs each run of deletions with the additions that follow it. */
function splitRows(f: FileDiff): Row[] {
  const rows: Row[] = [];
  for (const h of f.hunks) {
    rows.push({ k: "hunk", text: h.header });
    let dels: DiffLine[] = [];
    let adds: DiffLine[] = [];
    const flush = () => {
      for (let i = 0; i < Math.max(dels.length, adds.length); i++) rows.push({ k: "pair", l: dels[i], r: adds[i] });
      dels = [];
      adds = [];
    };
    for (const line of h.lines) {
      if (line.type === "delete") {
        if (adds.length) flush();
        dels.push(line);
      } else if (line.type === "add") adds.push(line);
      else {
        flush();
        if (line.type === "context") rows.push({ k: "pair", l: line, r: line });
        else {
          // "no newline" marker: show it on the side of the line it follows.
          const prev = h.lines[h.lines.indexOf(line) - 1];
          rows.push(prev?.type === "add" ? { k: "pair", r: line } : prev?.type === "delete" ? { k: "pair", l: line } : { k: "pair", l: line, r: line });
        }
      }
    }
    flush();
  }
  return rows;
}

const cls = (l: DiffLine | undefined) =>
  !l ? "dv-none" : l.type === "add" ? "dv-add" : l.type === "delete" ? "dv-del" : l.type === "no-newline" ? "dv-nn" : "";
const mark = (l: DiffLine) => (l.type === "add" ? "+" : l.type === "delete" ? "−" : "");
const num = (n: number) => (n > 0 ? n : "");

function Side({ line }: { line?: DiffLine }) {
  const c = cls(line);
  const n = line ? (line.type === "add" ? line.newLine : line.oldLine || line.newLine) : 0;
  return (
    <>
      <span className={`dv-ln ${c}`}>{line && line.type !== "no-newline" ? num(n) : ""}</span>
      <span className={`dv-mk ${c}`}>{line ? mark(line) : ""}</span>
      <span className={`dv-tx ${c}`}>{line?.text ?? ""}</span>
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
          <span className="dv-tx">{l.text}</span>
        </div>,
      );
    } else {
      out.push(
        <div key={i} className="dv-row dv-pair">
          <Side line={r.l} />
          <Side line={r.r} />
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

export const filePath = (f: FileDiff) => (f.status === "deleted" ? f.oldPath : f.newPath);

function FileCard({ file, index, mode, actions }: { file: FileDiff; index: number; mode: DiffMode; actions?: ReactNode }) {
  const t = useT("code");
  const [open, setOpen] = useState(file.status !== "deleted");
  const renamed = (file.status === "renamed" || file.status === "copied") && file.oldPath !== file.newPath;
  return (
    <section className="ui-card dv-file" id={`diff-${index}`}>
      <div className="dv-fh">
        <button type="button" className="ui-iconbtn" aria-expanded={open} aria-label={open ? t("diff.collapse") : t("diff.expand")} onClick={() => setOpen(!open)}>
          <Icon name={open ? "down" : "right"} />
        </button>
        <span className="dv-path">
          {renamed ? `${file.oldPath} → ${file.newPath}` : filePath(file)}
        </span>
        {file.status !== "modified" && <span className="dv-status">{t(`diff.status.${file.status}`)}</span>}
        <span className="dv-p">+{file.additions}</span>
        <span className="dv-m">−{file.deletions}</span>
        {actions}
      </div>
      {open && <FileBody file={file} mode={mode} />}
    </section>
  );
}

/** Diff of several files: file list on the left (desktop), one card per file. */
export function DiffView({ diff, fileActions }: { diff: Diff; fileActions?: (f: FileDiff) => ReactNode }) {
  const t = useT("code");
  const [mode, setMode, narrow] = useDiffMode();
  if (diff.files.length === 0) return <p className="ui-note acc-pad">{t("diff.empty")}</p>;
  const jump = (i: number) => document.getElementById(`diff-${i}`)?.scrollIntoView({ block: "start" });
  return (
    <div className="dv-split-layout">
      <Card className="dv-files" title={t("diff.files", { count: diff.files.length })}>
        <div className="dv-ft">
          {diff.files.map((f, i) => (
            <a
              key={i}
              href={`#diff-${i}`}
              onClick={(e) => {
                e.preventDefault();
                jump(i);
              }}
            >
              <Icon name="file" />
              <span>{filePath(f).split("/").pop()}</span>
              <b className="dv-p">+{f.additions}</b>
              <b className="dv-m">{f.deletions ? `−${f.deletions}` : ""}</b>
            </a>
          ))}
        </div>
      </Card>
      <div className="dv-main">
        <div className="dv-bar">
          <span className="ui-note">
            {t("diff.summary", { files: diff.files.length })} <span className="dv-p">+{diff.additions}</span> <span className="dv-m">−{diff.deletions}</span>
          </span>
          {!narrow && (
            <div className="ui-tablist" role="group" aria-label={t("diff.mode")}>
              {(["unified", "split"] as const).map((m) => (
                <button key={m} type="button" className="ui-tab" data-state={mode === m ? "active" : "inactive"} aria-pressed={mode === m} onClick={() => setMode(m)}>
                  {t(`diff.mode.${m}`)}
                </button>
              ))}
            </div>
          )}
        </div>
        {diff.truncated && <p className="ui-note acc-pad" role="status">{t("diff.truncated")}</p>}
        {diff.files.map((f, i) => (
          <FileCard key={`${i}-${mode}`} file={f} index={i} mode={mode} actions={fileActions?.(f)} />
        ))}
      </div>
    </div>
  );
}
