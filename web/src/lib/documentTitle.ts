import { useEffect, useSyncExternalStore } from "react";

// The tab title is "<badge> <page> · mangarr": pages name themselves, the
// layout adds a badge (the queue count) while downloads are waiting.
let page = "";
let badge = "";
let site = "mangarr";

const listeners = new Set<() => void>();

function apply() {
  document.title = [badge, page ? `${page} · ${site}` : site].filter(Boolean).join(" ");
  for (const listener of listeners) listener();
}

/** usePageTitle is the current page's own name ("" when it has none), for the phone's top bar. */
export function usePageTitle() {
  return useSyncExternalStore((listener) => {
    listeners.add(listener);
    return () => { listeners.delete(listener); };
  }, () => page);
}

/** useDocumentTitle names the current page in the browser tab and history. */
export function useDocumentTitle(title?: string) {
  useEffect(() => {
    if (!title) return;
    page = title;
    apply();
    return () => {
      if (page === title) page = "";
      apply();
    };
  }, [title]);
}

/** useTitleBadge puts a short marker, such as "(3)", in front of the title. */
export function useTitleBadge(value: string) {
  useEffect(() => {
    badge = value;
    apply();
    return () => {
      badge = "";
      apply();
    };
  }, [value]);
}

/** setSiteName replaces "mangarr" with the instance name. */
export function setSiteName(name: string) {
  site = name.trim() || "mangarr";
  apply();
}
