import type { ReactNode } from "react";
import { ToastProvider } from "../Toast.tsx";
import { ConsoleShell } from "./ConsoleShell.tsx";
import { CorsieShell } from "./CorsieShell.tsx";
import { FoglioShell } from "./FoglioShell.tsx";
import { useLayout } from "./layout.ts";

/** Picks the shell for the active layout (data-layout on <html>). */
export function AppShell({ children }: { children: ReactNode }) {
  const layout = useLayout();
  const Shell = layout === "foglio" ? FoglioShell : layout === "corsie" ? CorsieShell : ConsoleShell;
  return (
    <ToastProvider>
      <Shell>{children}</Shell>
    </ToastProvider>
  );
}
