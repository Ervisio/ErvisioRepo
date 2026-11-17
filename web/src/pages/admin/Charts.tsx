import { useState, type ReactNode } from "react";

// Small charts of the admin overview: one series each (area or stacked bars), a headline value, two axis
// labels, a hover readout and a visually hidden table with the same numbers.

export type ChartRow = { label: string; value: string };

function ChartFrame({ title, value, axis, rows, tip, onLeave, children }: {
  title: string;
  value: string;
  axis: [ReactNode, ReactNode];
  rows: ChartRow[];
  tip?: ChartRow;
  onLeave: () => void;
  children: ReactNode;
}) {
  return (
    <figure className="ao-ch" onPointerLeave={onLeave}>
      <figcaption className="ao-ch-k">
        <span>{title}</span>
        <b>{value}</b>
      </figcaption>
      <div className="ao-ch-p" aria-hidden="true">
        {children}
        {tip && <span className="ao-tip"><b>{tip.value}</b> {tip.label}</span>}
      </div>
      <div className="ao-ch-ax" aria-hidden="true"><span>{axis[0]}</span><span>{axis[1]}</span></div>
      <table className="sr-only">
        <caption>{title}</caption>
        <tbody>
          {rows.map((r, i) => <tr key={i}><th scope="row">{r.label}</th><td>{r.value}</td></tr>)}
        </tbody>
      </table>
    </figure>
  );
}

/** Invisible columns over the plot that pick the bucket under the pointer. */
function HitColumns({ n, onPick }: { n: number; onPick: (i: number) => void }) {
  return (
    <div className="ao-hit">
      {Array.from({ length: n }, (_, i) => <span key={i} onPointerEnter={() => onPick(i)} />)}
    </div>
  );
}

const H = 40;

/** Area of one series over evenly spaced points. */
export function AreaChart({ title, value, axis, values, rows, max }: {
  title: string;
  value: string;
  axis: [ReactNode, ReactNode];
  values: number[];
  rows: ChartRow[];
  /** Top of the scale; defaults to the largest value. */
  max?: number;
}) {
  const [hover, setHover] = useState<number>();
  const top = Math.max(max ?? 0, ...values, 1);
  const step = values.length > 1 ? 100 / (values.length - 1) : 100;
  const pts = values.map((v, i) => `${(i * step).toFixed(2)} ${(H - (v / top) * (H - 2)).toFixed(2)}`);
  const line = pts.length ? `M${pts.join(" L")}` : "";
  const area = pts.length ? `${line} L${((values.length - 1) * step).toFixed(2)} ${H} L0 ${H}Z` : "";
  return (
    <ChartFrame title={title} value={value} axis={axis} rows={rows} tip={hover === undefined ? undefined : rows[hover]} onLeave={() => setHover(undefined)}>
      <svg viewBox={`0 0 100 ${H}`} preserveAspectRatio="none">
        <path d={area} className="ao-area" />
        <path d={line} className="ao-line" vectorEffect="non-scaling-stroke" />
        {hover !== undefined && <line x1={hover * step} x2={hover * step} y1={0} y2={H} className="ao-cross" vectorEffect="non-scaling-stroke" />}
      </svg>
      <HitColumns n={values.length} onPick={setHover} />
    </ChartFrame>
  );
}

/** Bars of successes with failures stacked on top (status colours, 2 px gap between them). */
export function JobBars({ title, value, axis, bars, rows }: {
  title: string;
  value: string;
  axis: [ReactNode, ReactNode];
  bars: { success: number; failure: number }[];
  rows: ChartRow[];
}) {
  const [hover, setHover] = useState<number>();
  const top = Math.max(1, ...bars.map((b) => b.success + b.failure));
  const w = 100 / Math.max(bars.length, 1);
  const scale = (n: number) => (n / top) * (H - 2);
  return (
    <ChartFrame title={title} value={value} axis={axis} rows={rows} tip={hover === undefined ? undefined : rows[hover]} onLeave={() => setHover(undefined)}>
      <svg viewBox={`0 0 100 ${H}`} preserveAspectRatio="none">
        {bars.map((b, i) => {
          const ok = scale(b.success);
          const bad = scale(b.failure);
          const x = i * w + w * 0.15;
          const bw = w * 0.7;
          return (
            <g key={i} className={hover === i ? "ao-bar on" : "ao-bar"}>
              {ok > 0 && <rect x={x} y={H - ok} width={bw} height={ok} className="ao-ok" />}
              {bad > 0 && <rect x={x} y={Math.max(0, H - ok - bad - (ok > 0 ? 1 : 0))} width={bw} height={bad} className="ao-fail" />}
            </g>
          );
        })}
      </svg>
      <HitColumns n={bars.length} onPick={setHover} />
    </ChartFrame>
  );
}
