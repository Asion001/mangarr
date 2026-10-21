import { useNavigate } from "react-router";

/**
 * parentOf is the page a page belongs under, for its Back button: a title
 * under the library, a settings page under Settings, and so on. Top-level
 * pages have none.
 */
export function parentOf(pathname: string): string | null {
  const rules: [RegExp, string][] = [
    [/^\/series\/[^/]+/, "/"],
    [/^\/settings\/[^/]+/, "/settings"],
    [/^\/discover\/[^/]+/, "/discover"],
    [/^\/import\/[^/]+/, "/import"],
    [/^\/add\/.+/, "/add"],
  ];
  return rules.find(([re]) => re.test(pathname))?.[1] ?? null;
}

/**
 * useBack goes back to where you came from inside the app, or to the parent
 * page when the page was opened directly (a link, a reload, the home screen),
 * where history back would leave the app.
 */
export function useBack(parent: string) {
  const navigate = useNavigate();
  return () => {
    const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0;
    if (idx > 0) navigate(-1);
    else navigate(parent, { replace: true });
  };
}
