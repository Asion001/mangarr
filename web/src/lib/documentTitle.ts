import { useEffect } from "react";

// The tab title is "<badge> <page> · mangarr": pages name themselves, the
// layout adds a badge (the queue count) while downloads are waiting.
let page = "";
let badge = "";
let site = "mangarr";

function apply() {
  document.title = [badge, page ? `${page} · ${site}` : site].filter(Boolean).join(" ");
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
