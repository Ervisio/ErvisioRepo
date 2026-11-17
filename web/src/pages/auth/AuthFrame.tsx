import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useMeta } from "../../api/useMeta.ts";
import { useT } from "../../i18n/useT.ts";

/**
 * Page chrome for everything visible before sign-in: logo, title, the form in a card, a box with
 * the secondary link and the instance version. No app header.
 */
export function AuthFrame({ title, subtitle, children, footer, wide }: {
  title: string;
  subtitle?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  /** 460px column instead of 400px (runner approval, OAuth consent). */
  wide?: boolean;
}) {
  const t = useT("shell");
  const meta = useMeta();
  return (
    <div className="auth sec-act">
      <main className={wide ? "auth-pg auth-wide" : "auth-pg"}>
        <Link to="/" className="auth-logo" aria-label={t("brand.home")}>E</Link>
        <div className="auth-at">
          <h1>{title}</h1>
          {subtitle && <p className="auth-sub">{subtitle}</p>}
          <div className="auth-box">
            <div className="auth-in">{children}</div>
          </div>
          {footer && <div className="auth-nx"><p className="auth-foot">{footer}</p></div>}
        </div>
        {meta.data && <footer className="auth-lf">{meta.data.name} {meta.data.version.replace(/^v?/, "v")}</footer>}
      </main>
    </div>
  );
}
