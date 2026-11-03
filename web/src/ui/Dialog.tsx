import * as RDialog from "@radix-ui/react-dialog";
import type { ReactNode } from "react";
import { useT } from "../i18n/useT.ts";
import { Icon } from "./Icon.tsx";

export type DialogProps = {
  trigger?: ReactNode;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  title: string;
  description?: string;
  children?: ReactNode;
  footer?: ReactNode;
};

export function Dialog({ trigger, open, onOpenChange, title, description, children, footer }: DialogProps) {
  const t = useT("ui");
  return (
    <RDialog.Root open={open} onOpenChange={onOpenChange}>
      {trigger && <RDialog.Trigger asChild>{trigger}</RDialog.Trigger>}
      <RDialog.Portal>
        <RDialog.Overlay className="ui-overlay" />
        <RDialog.Content className="ui-dialog" {...(description ? {} : { "aria-describedby": undefined })}>
          <div className="ui-dialog-head">
            <RDialog.Title>{title}</RDialog.Title>
            <RDialog.Close className="ui-iconbtn" aria-label={t("dialog.close")}>
              <Icon name="x" />
            </RDialog.Close>
          </div>
          {description && <RDialog.Description className="ui-dialog-desc">{description}</RDialog.Description>}
          {children && <div className="ui-dialog-body">{children}</div>}
          {footer && <div className="ui-dialog-foot">{footer}</div>}
        </RDialog.Content>
      </RDialog.Portal>
    </RDialog.Root>
  );
}

/** Closes the surrounding Dialog; wrap a Button. */
export const DialogClose = RDialog.Close;
