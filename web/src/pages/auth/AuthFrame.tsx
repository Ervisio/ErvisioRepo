import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useT } from "../../i18n/useT.ts";

/** Centered card for the pages visible before sign-in. */
export function AuthFrame({ title, subtitle, children, footer }: {
  title: string;
  subtitle?: string;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const t = useT("shell");
  return (
    <div className="auth sec-act">
      <main className="auth-card">
        <Link to="/" className="auth-brand" aria-label={t("brand.home")}>
          <span className="cs-logo" aria-hidden="true">E</span>
          <b>{t("brand.name")}</b>
        </Link>
        <h1>{title}</h1>
        {subtitle && <p className="auth-sub">{subtitle}</p>}
        {children}
        {footer && <p className="auth-foot">{footer}</p>}
      </main>
    </div>
  );
}
