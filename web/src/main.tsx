import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { router } from "./router.tsx";
import "./styles/tokens.css";
import "./styles/app.css";
import "./styles/auth.css";
import "./ui/ui.css";
import "./ui/code/code.css";
import "./styles/work.css";
import { initCspNonce } from "./ui/nonce.ts";
import { initLayout } from "./ui/shell/layout.ts";

initCspNonce();
initLayout();

const queryClient = new QueryClient();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
