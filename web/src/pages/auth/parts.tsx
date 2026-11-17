import { Fragment, useId, type ReactNode } from "react";

/**
 * One-time code field: a single text input (so autofill, paste and screen readers keep working)
 * drawn as a row of boxes. The boxes are decoration only; the transparent input sits on top.
 */
export function OtpInput({ label, value, onChange, length = 6 }: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  length?: number;
}) {
  const id = useId();
  return (
    <div className="auth-otp-wrap">
      <label htmlFor={id} className="sr-only">{label}</label>
      <div className="auth-otp">
        <input
          id={id}
          className="auth-otp-in"
          type="text"
          inputMode="numeric"
          autoComplete="one-time-code"
          pattern={`\\d{${length}}`}
          maxLength={length}
          autoFocus
          required
          spellCheck={false}
          value={value}
          onChange={(e) => onChange(e.target.value.replace(/\D/g, ""))}
        />
        {Array.from({ length }, (_, i) => (
          <span key={i} className="auth-otp-b" data-cur={i === Math.min(value.length, length - 1) ? "" : undefined} aria-hidden="true">
            {value[i]}
          </span>
        ))}
      </div>
    </div>
  );
}

/** Read-only device code drawn as boxes, split in two groups at the dash (K7QM-X4TP). */
export function CodeBoxes({ code, label }: { code: string; label: string }) {
  return (
    <div className="auth-code" role="img" aria-label={`${label}: ${code}`}>
      {code.split("-").map((group, gi) => (
        <Fragment key={gi}>
          {gi > 0 && <i className="auth-code-d" aria-hidden="true" />}
          {[...group].map((c, i) => <span key={i} className="auth-code-b" aria-hidden="true">{c}</span>)}
        </Fragment>
      ))}
    </div>
  );
}

/** Label / value rows separated by hairlines. */
export function Details({ rows }: { rows: { label: string; value: ReactNode; mono?: boolean }[] }) {
  return (
    <dl className="auth-kv">
      {rows.map((r) => (
        <div key={r.label}>
          <dt>{r.label}</dt>
          <dd className={r.mono ? "auth-mono" : undefined}>{r.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Horizontal rule with a word in the middle. */
export function Or({ children }: { children: ReactNode }) {
  return <div className="auth-sep" role="separator"><span>{children}</span></div>;
}
