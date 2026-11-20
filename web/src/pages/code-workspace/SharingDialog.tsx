import { Close, Content, Description, Overlay, Portal, Root, Title } from "@radix-ui/react-dialog";
import { useRef, useState, type ReactNode, type RefObject } from "react";
import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../../ui/Icon.tsx";

export function SharingDialog({ title, description, busy, onClose, onRestoreFocus, initialFocus, children }: {
  title: string;
  description: string;
  busy: boolean;
  onClose: () => void;
  onRestoreFocus: () => void;
  initialFocus?: RefObject<HTMLElement | null>;
  children: ReactNode;
}) {
  const t = useT("codehub");
  return (
    <Root open onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <Portal>
        <Overlay className="ec-drawer-overlay ec-new-overlay" />
        <Content className="ec-new ec-sharing" onOpenAutoFocus={(event) => {
          if (!initialFocus?.current) return;
          event.preventDefault();
          initialFocus.current.focus();
        }} onCloseAutoFocus={(event) => {
          event.preventDefault();
          onRestoreFocus();
        }}>
          <div className="ec-new-head">
            <div>
              <Title>{title}</Title>
              <Description>{description}</Description>
            </div>
            <Close className="ec-icon-button" aria-label={t("share.close")} disabled={busy}><Icon name="x" /></Close>
          </div>
          {children}
        </Content>
      </Portal>
    </Root>
  );
}

export function useSharingAction(refresh: () => Promise<unknown>) {
  const active = useRef(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();

  async function run(mutate: () => Promise<void>) {
    if (active.current) return;
    active.current = true;
    setBusy(true);
    setProblem(undefined);
    try {
      await mutate();
    } catch (error) {
      setProblem(sharingError(error));
    } finally {
      try {
        await refresh();
      } finally {
        active.current = false;
        setBusy(false);
      }
    }
  }

  return { busy, problem, run };
}

function sharingError(error: unknown) {
  if (!(error instanceof ApiError) || error.status >= 500 || error.status < 400) return "share.uncertain";
  if (error.status === 401 || error.status === 403) return "share.denied";
  if (error.status === 404 || error.status === 409 || error.status === 410) return "share.unavailable";
  if (error.status === 429) return "share.rateLimited";
  return "share.rejected";
}
