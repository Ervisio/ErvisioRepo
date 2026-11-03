import { useEffect, useMemo, type CSSProperties, type ReactNode } from "react";
import type { Token } from "./highlight.ts";
import { scrollToRow, useVirtualRows } from "./virtual.ts";

/** Row height in px; must match .cv-row in code.css (12.75px mono at line-height 1.65). */
export const ROW = 21;

/** Visual width of a line in columns (tabs at 4). */
export function columns(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) n += s.charCodeAt(i) === 9 ? 4 - (n % 4) : 1;
  return n;
}

export function TokenLine({ tokens, text }: { tokens?: Token[]; text: string }) {
  if (!tokens) return <>{text}</>;
  return (
    <>
      {tokens.map((t, i) =>
        t.color || t.italic || t.bold ? (
          <span key={i} style={{ color: t.color, fontStyle: t.italic ? "italic" : undefined, fontWeight: t.bold ? 700 : undefined }}>
            {t.content}
          </span>
        ) : (
          t.content
        ),
      )}
    </>
  );
}

/**
 * Read-only code with line numbers on --bg, virtualized above a few thousand lines.
 * `annotate` adds a fixed-width column before the numbers (used by blame).
 */
export function CodeView({ lines, tokens, selected, onSelect, annotate, label }: {
  lines: string[];
  tokens?: Token[][];
  selected?: number;
  onSelect?: (line: number) => void;
  annotate?: (index: number) => ReactNode;
  label: string;
}) {
  const { ref, start, end, height } = useVirtualRows(lines.length, ROW);
  const width = useMemo(() => lines.reduce((m, l) => Math.max(m, columns(l)), 0), [lines]);
  const digits = String(lines.length).length;

  useEffect(() => {
    if (selected && selected > 0) scrollToRow(ref.current, selected - 1, ROW);
    // Only on first render for a deep link; later selections come from a click on screen.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lines]);

  const style = { "--ln-w": `${digits + 2}ch`, "--cv-w": `${width + 2}ch`, height } as CSSProperties;
  const rows: ReactNode[] = [];
  for (let i = start; i < end; i++) {
    const n = i + 1;
    rows.push(
      <div key={i} className={n === selected ? "cv-row cv-sel" : "cv-row"} id={`L${n}`}>
        {annotate && <span className="cv-ann">{annotate(i)}</span>}
        <a
          className="cv-ln"
          href={`#L${n}`}
          onClick={(e) => {
            if (!onSelect) return;
            e.preventDefault();
            onSelect(n);
          }}
        >
          {n}
        </a>
        <span className="cv-tx">
          <TokenLine tokens={tokens?.[i]} text={lines[i] ?? ""} />
        </span>
      </div>,
    );
  }
  return (
    <div className={annotate ? "cv cv-annotated" : "cv"} role="region" aria-label={label} tabIndex={0}>
      <div ref={ref} className="cv-body" style={style}>
        <div className="cv-win" style={{ transform: `translateY(${start * ROW}px)` }}>
          {rows}
        </div>
      </div>
    </div>
  );
}
