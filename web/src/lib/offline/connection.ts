import { useSyncExternalStore } from "react";
import { basePath } from "../../api/client";

/**
 * Whether the server answers, as the service worker sees it (public/sw.js
 * posts a message when that changes) and the browser's own online state.
 * While it doesn't, a light request every few seconds notices it's back.
 */

let reachable = true;

// ?ping keeps the service worker from answering with its remembered copy
const ping = () =>
  fetch(`${basePath}/api/v1/auth/status?ping=1`, { cache: "no-store" })
    .then((r) => set(r.status < 500))
    .catch(() => set(false));
let timer = 0;
const listeners = new Set<() => void>();

function set(next: boolean) {
  if (next === reachable) return;
  reachable = next;
  window.clearInterval(timer);
  if (!next) timer = window.setInterval(() => void ping(), 5000);
  for (const l of listeners) l();
}

if (typeof window !== "undefined") {
  navigator.serviceWorker?.addEventListener("message", (e: MessageEvent) => {
    const d = e.data as { type?: string; reachable?: boolean } | undefined;
    if (d?.type === "mangarr:reachable") set(!!d.reachable);
  });
  window.addEventListener("offline", () => set(false));
  window.addEventListener("online", () => void ping());
  // a page opened while the server is away is told so straight away
  if (navigator.serviceWorker?.controller) void ping();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** useReachable is false while the server doesn't answer. */
export const useReachable = () => useSyncExternalStore(subscribe, () => reachable);
