import type { ReactNode } from "react";
import type { Section } from "./nav.tsx";

/**
 * A page declares its zones (title, actions, content, aside); the active shell and the
 * data-layout CSS arrange them. `section` sets --hue / --hue-s for the whole page.
 */
export function Page({ section = "code", title, subtitle, actions, aside, children }: {
  section?: Section;
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  aside?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className={`page sec-${section}`}>
      <header className="page-head">
        <h1>{title}</h1>
        {subtitle && <span className="page-sub">{subtitle}</span>}
        {actions && <div className="page-actions">{actions}</div>}
      </header>
      <div className={aside ? "page-body page-body-aside" : "page-body"}>
        <div className="page-main">{children}</div>
        {aside && <aside className="page-aside">{aside}</aside>}
      </div>
    </div>
  );
}
