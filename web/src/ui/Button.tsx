import type { ButtonHTMLAttributes } from "react";
import { Icon, type IconName } from "./Icon.tsx";

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  /** "primary" is the single filled CTA of a view (section hue, black text). */
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "md" | "sm";
  icon?: IconName;
};

export function Button({ variant = "secondary", size = "md", icon, className, children, type = "button", ...rest }: ButtonProps) {
  const cls = ["ui-btn", `ui-btn-${variant}`, size === "sm" ? "ui-btn-sm" : "", className].filter(Boolean).join(" ");
  return (
    <button type={type} className={cls} {...rest}>
      {icon && <Icon name={icon} />}
      {children}
    </button>
  );
}
