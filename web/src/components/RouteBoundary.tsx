import { t } from "../lib/i18n/core";
import { Component, Suspense, type ErrorInfo, type ReactNode } from "react";
import { useLocation } from "react-router";
import { RefreshCw, RotateCcw } from "lucide-react";
import { Button, Loading } from "./ui";

/** A lazily loaded page whose file is gone: the server was updated since this tab loaded. */
export function isStaleChunk(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return /dynamically imported module|Importing a module script failed|error loading dynamically imported|Failed to fetch dynamically/i.test(message);
}

type Props = { children: ReactNode; resetKey: string };
type State = { error: unknown };

class Boundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: unknown): State {
    return { error };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("page failed", error, info.componentStack);
  }

  componentDidUpdate(prev: Props) {
    // leaving the broken page clears the error
    if (prev.resetKey !== this.props.resetKey && this.state.error !== null) this.setState({ error: null });
  }

  render() {
    const { error } = this.state;
    if (error === null) return this.props.children;
    const stale = isStaleChunk(error);
    return (
      <div role="alert" className="mx-auto my-10 flex max-w-lg flex-col items-center gap-3 rounded-lg border border-border bg-panel p-6 text-center">
        <h2 className="font-semibold">{stale ? t("mangarr was updated") : t("This page failed to load")}</h2>
        <p className="text-sm text-muted">
          {stale ? t("Reload to get the new version of this page.") : error instanceof Error ? error.message : String(error)}
        </p>
        <div className="flex gap-2">
          {!stale && <Button icon={<RotateCcw className="size-4" />} onClick={() => this.setState({ error: null })}>{t("Try again")}</Button>}
          <Button variant="primary" icon={<RefreshCw className="size-4" />} onClick={() => window.location.reload()}>{t("Reload")}</Button>
        </div>
      </div>
    );
  }
}

/**
 * RouteBoundary shows a spinner while a page's code loads, and a way out
 * (retry, reload) when a page throws, instead of a blank screen.
 */
export function RouteBoundary({ children }: { children: ReactNode }) {
  const { pathname } = useLocation();
  return (
    <Boundary resetKey={pathname}>
      <Suspense fallback={<Loading />}>{children}</Suspense>
    </Boundary>
  );
}
