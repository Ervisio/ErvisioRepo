import type { ReactNode } from "react";
import { Icon, type IconName } from "./Icon.tsx";

export function EmptyState({ icon = "layers", title, description, action }: {
  icon?: IconName;
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="ui-empty">
      <span className="ui-empty-icon">
        <Icon name={icon} />
      </span>
      <h3>{title}</h3>
      {description && <p>{description}</p>}
      {action}
    </div>
  );
}
