import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";
import "./index.css";
import { UIPreferencesProvider } from "./lib/uiPreferences";
import { App } from "./App";
import { ToastProvider } from "./lib/toast";
import { basePath } from "./api/client";
import { subscribeLocale } from "./lib/i18n/core";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 10_000, retry: (count, err) => count < 2 && !(err as { status?: number })?.status?.toString().startsWith("4"), refetchOnWindowFocus: true },
  },
});

// titles come back in the interface language, so a new one refetches them
subscribeLocale(() => void queryClient.invalidateQueries());

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <BrowserRouter basename={basePath || "/"}>
          <UIPreferencesProvider><App /></UIPreferencesProvider>
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
);
