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

// series titles come back in the interface language, so a new one refetches
// them (only them: refetching the preferences mid-save would undo the switch)
subscribeLocale(() => void queryClient.invalidateQueries({ queryKey: ["series"] }));

// In a home screen app #root takes the page's scrolling (index.css) but has
// nothing of its own to scroll: pages scroll inside the layout. When iOS
// moves it anyway the whole layout slides up under the status bar, header
// and all, so put it straight back.
const root = document.getElementById("root")!;
root.addEventListener("scroll", () => { if (root.scrollTop) root.scrollTop = 0; }, { passive: true });

createRoot(root).render(
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
