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

export function MenuItem({ icon, danger, children, onSelect, asChild, className }: {
  icon?: IconName;
  danger?: boolean;
  children: ReactNode;
  onSelect?: (event: Event) => void;
  asChild?: boolean;
  className?: string;
}) {
  const cls = ["ui-item", danger ? "ui-item-danger" : "", className].filter(Boolean).join(" ");
  return (
    <RMenu.Item className={cls} onSelect={onSelect} asChild={asChild}>
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

/** Single choice inside a menu; selecting an option keeps the menu open. */
export function MenuRadioGroup({ value, onValueChange, label, className, children }: { value: string; onValueChange: (v: string) => void; label?: string; className?: string; children: ReactNode }) {
  return (
    <RMenu.RadioGroup value={value} onValueChange={onValueChange} aria-label={label} className={className}>
      {children}
    </RMenu.RadioGroup>
  );
}

export function MenuRadioItem({ value, children, className }: { value: string; children: ReactNode; className?: string }) {
  return (
    <RMenu.RadioItem value={value} className={className} onSelect={(e) => e.preventDefault()}>
      {children}
    </RMenu.RadioItem>
  );
}
