import * as RMenu from "@radix-ui/react-dropdown-menu";
import type { ReactNode } from "react";
import { Icon, type IconName } from "./Icon.tsx";

export function Menu({ trigger, children, align = "start", className, label }: { trigger: ReactNode; children: ReactNode; align?: "start" | "end"; className?: string; label?: string }) {
  return (
    <RMenu.Root>
      <RMenu.Trigger asChild>{trigger}</RMenu.Trigger>
      <RMenu.Portal>
        <RMenu.Content className={className ? `ui-pop ${className}` : "ui-pop"} align={align} sideOffset={6} aria-label={label}>
          {children}
        </RMenu.Content>
      </RMenu.Portal>
    </RMenu.Root>
  );
}

export function MenuItem({ icon, danger, children, onSelect, asChild }: {
  icon?: IconName;
  danger?: boolean;
  children: ReactNode;
  onSelect?: (event: Event) => void;
  asChild?: boolean;
}) {
  return (
    <RMenu.Item className={danger ? "ui-item ui-item-danger" : "ui-item"} onSelect={onSelect} asChild={asChild}>
      {asChild ? (
        children
      ) : (
        <>
          {icon && <Icon name={icon} />}
          {children}
        </>
      )}
    </RMenu.Item>
  );
}

export function MenuLabel({ children }: { children: ReactNode }) {
  return <RMenu.Label className="ui-menu-label">{children}</RMenu.Label>;
}

export function MenuSeparator() {
  return <RMenu.Separator className="ui-menu-sep" />;
}
