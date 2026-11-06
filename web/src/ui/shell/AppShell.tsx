import type { ReactNode } from "react";
import { ToastProvider } from "../Toast.tsx";
import { ConsoleShell, type ShellDialogs } from "./ConsoleShell.tsx";
import { CorsieShell } from "./CorsieShell.tsx";
import { FoglioShell } from "./FoglioShell.tsx";
import { useLayout } from "./layout.ts";
import { SearchPaletteProvider } from "./SearchPalette.tsx";
import { useAppearance } from "./useAppearance.ts";

/** Picks the shell for the active layout (data-layout on <html>). */
export function AppShell({ children, dialogs }: { children: ReactNode; dialogs?: ShellDialogs }) {
  useAppearance();
  const layout = useLayout();
  return (
    <ToastProvider>
      <SearchPaletteProvider>
        {layout === "foglio" ? (
          <FoglioShell>{children}</FoglioShell>
        ) : layout === "corsie" ? (
          <CorsieShell>{children}</CorsieShell>
        ) : (
          <ConsoleShell dialogs={dialogs}>{children}</ConsoleShell>
        )}
      </SearchPaletteProvider>
    </ToastProvider>
  );
}
