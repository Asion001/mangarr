import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";
import "./index.css";
import { UIPreferencesProvider } from "./lib/uiPreferences";
import { App } from "./App";
import { Reconnecting } from "./components/Reconnecting";
import { ToastProvider } from "./lib/toast";
import { basePath } from "./api/client";
import { subscribeLocale } from "./lib/i18n/core";
import { isServerDown, onServerBack, unreachable } from "./lib/connection";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      // while mangarr restarts, keep trying (about five minutes) instead of
      // showing an error; a page keeps what it had meanwhile. A proxy's
      // timeout on a slow request while mangarr is up retries as usual.
      retry: (count, err) => (unreachable(err) && isServerDown() ? count < 35 : count < 2 && !(err as { status?: number })?.status?.toString().startsWith("4")),
      retryDelay: (count, err) => Math.min(1000 * 2 ** count, unreachable(err) ? 10_000 : 30_000),
      refetchOnWindowFocus: true,
    },
  },
});

// series titles come back in the interface language, so a new one refetches
// them (only them: refetching the preferences mid-save would undo the switch)
subscribeLocale(() => void queryClient.invalidateQueries({ queryKey: ["series"] }));

// back after a restart: whatever was waiting to retry goes now
onServerBack(() => void queryClient.refetchQueries({ type: "active", predicate: (q) => q.state.fetchFailureCount > 0 || q.state.status === "error" }));

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
          <Reconnecting />
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
);
