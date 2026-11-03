import * as RToast from "@radix-ui/react-toast";
import { createContext, use, useCallback, useMemo, useState, type ReactNode } from "react";
import { useT } from "../i18n/useT.ts";
import { Icon } from "./Icon.tsx";
import type { BadgeTone } from "./Badge.tsx";

export type ToastInput = { title: string; description?: string; tone?: Exclude<BadgeTone, "section"> };
type Item = ToastInput & { id: number };

const ToastContext = createContext<(t: ToastInput) => void>(() => {});

/** Returns a function that shows a toast. Needs <ToastProvider> above. */
export function useToast() {
  return use(ToastContext);
}

let nextId = 1;

export function ToastProvider({ children }: { children: ReactNode }) {
  const t = useT("ui");
  const [items, setItems] = useState<Item[]>([]);
  const push = useCallback((input: ToastInput) => setItems((cur) => [...cur, { ...input, id: nextId++ }]), []);
  const value = useMemo(() => push, [push]);
  return (
    <ToastContext value={value}>
      <RToast.Provider swipeDirection="right" duration={5000} label={t("toast.region")}>
        {children}
        {items.map((it) => (
          <RToast.Root
            key={it.id}
            className={`ui-toast ui-toast-${it.tone ?? "neutral"}`}
            onOpenChange={(open) => {
              if (!open) setItems((cur) => cur.filter((x) => x.id !== it.id));
            }}
          >
            <div>
              <RToast.Title className="ui-toast-title">{it.title}</RToast.Title>
              {it.description && <RToast.Description className="ui-toast-desc">{it.description}</RToast.Description>}
            </div>
            <RToast.Close className="ui-iconbtn" aria-label={t("toast.dismiss")}>
              <Icon name="x" />
            </RToast.Close>
          </RToast.Root>
        ))}
        <RToast.Viewport className="ui-toast-viewport" />
      </RToast.Provider>
    </ToastContext>
  );
}
