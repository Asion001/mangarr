import { t } from "../lib/i18n/core";
import { Component, Suspense, type ErrorInfo, type ReactNode } from "react";
import { useLocation } from "react-router";
import { RefreshCw, RotateCcw } from "lucide-react";
import { Button, Loading } from "./ui";
import { checkServer, isServerDown, onServerBack } from "../lib/connection";

/** A lazily loaded page whose file is gone: the server was updated since this tab loaded. */
export function isStaleChunk(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return /dynamically imported module|Importing a module script failed|error loading dynamically imported|Failed to fetch dynamically/i.test(message);
}

/** chunkUrl is the file a failed page load names (where the browser says it). */
export function chunkUrl(error: unknown): string | undefined {
  const message = error instanceof Error ? error.message : String(error);
  return /(https?:\/\/\S+?\.js)\b/.exec(message)?.[1];
}

/**
 * retryWhenBack loads a page's code again once mangarr answers, when it
 * failed because mangarr was restarting. The browser remembers a failed
 * load, so the file is asked for under a fresh address. When mangarr answers
 * and the file is still missing it was updated, and the page offers a reload.
 */
export async function retryWhenBack<M, T>(load: () => Promise<M>, pick: (m: M) => T): Promise<T> {
  try {
    return pick(await load());
  } catch (e) {
    const url = chunkUrl(e);
    if (!isStaleChunk(e) || !url) throw e;
    await checkServer();
    if (!isServerDown()) throw e;
    await new Promise<void>((resolve) => {
      const off = onServerBack(() => {
        off();
        resolve();
      });
    });
    try {
      return pick(await import(/* @vite-ignore */ `${url}?retry=${Date.now()}`));
    } catch (again) {
      // a file it needs failed too, and the browser remembers that: start
      // the app afresh (once a minute at most, should it keep failing)
      if (reloadOnce()) await new Promise(() => undefined);
      throw again;
    }
  }
}

function reloadOnce(): boolean {
  try {
    const last = Number(sessionStorage.getItem("mangarr:chunk-reload") ?? 0);
    if (Date.now() - last < 60_000) return false;
    sessionStorage.setItem("mangarr:chunk-reload", String(Date.now()));
  } catch {
    return false;
  }
  window.location.reload();
  return true;
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
