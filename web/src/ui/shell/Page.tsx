import type { ReactNode } from "react";
import type { Section } from "./nav.tsx";

/**
 * A page declares its zones (title, actions, content, aside); the active shell and the
 * data-layout CSS arrange them. `section` sets --hue / --hue-s for the whole page.
 * `quiet` keeps the title for assistive tech only (the shell already shows it); the header
 * is then drawn only when there are actions or a subtitle.
 */
export function Page({ section = "code", title, subtitle, actions, aside, quiet, children }: {
  section?: Section;
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  aside?: ReactNode;
  quiet?: boolean;
  children: ReactNode;
}) {
  const heading = <h1 className={quiet ? "sr-only" : undefined}>{title}</h1>;
  return (
    <div className={quiet ? `page page-flush sec-${section}` : `page sec-${section}`}>
      {quiet && !actions && !subtitle ? (
        heading
      ) : (
        <header className="page-head">
          {heading}
          {subtitle && <span className="page-sub">{subtitle}</span>}
          {actions && <div className="page-actions">{actions}</div>}
        </header>
      )}
      <div className={aside ? "page-body page-body-aside" : "page-body"}>
        <div className="page-main">{children}</div>
        {aside && <aside className="page-aside">{aside}</aside>}
      </div>
    </div>
  );
}
