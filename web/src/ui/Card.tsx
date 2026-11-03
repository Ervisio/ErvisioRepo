import type { ComponentProps, ReactNode } from "react";

/** Content card on --sunk (radius 18, padding 6). Rows inside use .ui-row. */
export function Card({ title, actions, children, className, ...rest }: {
  title?: string;
  actions?: ReactNode;
} & ComponentProps<"section">) {
  return (
    <section className={["ui-card", className].filter(Boolean).join(" ")} {...rest}>
      {(title || actions) && (
        <div className="ui-card-head">
          {title && <h3>{title}</h3>}
          {actions && <div className="ui-card-actions">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  );
}
