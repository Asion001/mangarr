import { useEffect, useSyncExternalStore } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { basePath } from "../api/client";

// resource name (from the server) -> query key prefixes to invalidate
const map: Record<string, string[][]> = {
  series: [["series"], ["wanted"], ["calendar"], ["discover"], ["updates"]],
  chapter: [["series"], ["wanted"], ["discover"], ["updates"]],
  seriessource: [["series"]],
  queue: [["queue"], ["series"]],
  command: [["commands"], ["tasks"]],
  tasks: [["tasks"], ["settings", "readsync"]],
  health: [["health"]],
  module: [["modules"], ["sources"], ["health"], ["me-notifications"], ["me-library-accounts"]],
  extension: [["extensions"], ["sources"]],
  // catalog set changed: cached searches/browses may include removed catalogs
  catalogs: [["sources"], ["catalogs"], ["source-search"], ["browse"], ["source-manga"], ["discover"]],
  cache: [["cache"], ["source-search"], ["browse"], ["source-manga"], ["discover"]],
  processing: [["processing"], ["health"]],
  settings: [["settings"]],
  readers: [["readers"], ["series"], ["me-library-accounts"]],
  reading: [["reading"], ["settings"], ["discover"], ["updates"]],
  database: [["database"]],
  users: [["users"], ["auth"]],
  request: [["requests"], ["lookup"]],
  follow: [["series"], ["follows"], ["discover"]],
  rootfolder: [["rootfolders"], ["discover"]],
  profile: [["profiles"]],
  tag: [["tags"]],
  blocklist: [["blocklist"]],
  import: [["imports"], ["import"], ["import-entries"]],
  messenger: [["me-messenger"]],
};

type Listener = (type: string, payload: unknown) => void;
const listeners = new Set<Listener>();

export type LiveUpdateStatus = "connecting" | "connected" | "reconnecting" | "offline";
let liveStatus: LiveUpdateStatus = "connecting";
const statusListeners = new Set<() => void>();

function setLiveStatus(status: LiveUpdateStatus) {
  if (status === liveStatus) return;
  liveStatus = status;
  statusListeners.forEach((listener) => listener());
}

/** useLiveUpdateStatus exposes the SSE connection state to reader-facing pages. */
export function useLiveUpdateStatus() {
  return useSyncExternalStore(
    (listener) => {
      statusListeners.add(listener);
      return () => statusListeners.delete(listener);
    },
    () => liveStatus,
    () => "connecting" as const,
  );
}

/** Subscribe to raw server events (e.g. to show toasts). */
export function onServerEvent(fn: Listener) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** useLiveUpdates keeps TanStack Query caches fresh from the SSE stream. */
export function useLiveUpdates(enabled: boolean) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!enabled) return;
    const pending = new Map<string, string[]>();
    let timer: number | undefined;
    const flush = () => {
      timer = undefined;
      for (const key of pending.values()) qc.invalidateQueries({ queryKey: key });
      pending.clear();
    };
    let opened = false;
    let interrupted = false;
    let closed = false;
    let es: EventSource;
    let retry: number | undefined;
    let delay = 1000;
    setLiveStatus(navigator.onLine === false ? "offline" : "connecting");
    const connect = () => {
      retry = undefined;
      es = new EventSource(basePath + "/api/v1/events");
      es.onopen = () => {
        const reconnected = opened || interrupted;
        opened = true;
        interrupted = false;
        delay = 1000;
        setLiveStatus("connected");
        // Queries replace their cached result, so a reconnect catches missed
        // events without appending or duplicating feed rows.
        if (reconnected) void qc.invalidateQueries();
      };
      es.addEventListener("resource.changed", (ev) => {
        try {
          const e = JSON.parse((ev as MessageEvent).data);
          for (const key of map[e.payload?.name] ?? []) pending.set(key.join("/"), key);
          if (!timer) timer = window.setTimeout(flush, 400);
        } catch {
          /* ignore */
        }
      });
      for (const t of ["hello", "chapter.imported", "download.failed", "health.issue", "health.restored", "cleanup.done", "series.added", "processing.progress"]) {
        es.addEventListener(t, (ev) => {
          try {
            const e = JSON.parse((ev as MessageEvent).data);
            listeners.forEach((l) => l(t, e.payload));
          } catch {
            /* ignore */
          }
        });
      }
      es.onerror = () => {
        interrupted = true;
        setLiveStatus(navigator.onLine === false ? "offline" : "reconnecting");
        // The browser retries a dropped stream itself, but gives up for good
        // after an error response (a proxy's 502 while mangarr restarts):
        // start a new one, backing off.
        if (es.readyState === 2 /* CLOSED */ && !closed && retry === undefined) {
          retry = window.setTimeout(connect, delay);
          delay = Math.min(delay * 2, 30000);
        }
      };
    };
    connect();
    const offline = () => setLiveStatus("offline");
    // coming back online doesn't always drop the stream (a LAN server)
    const online = () => setLiveStatus(interrupted ? "reconnecting" : "connected");
    window.addEventListener("offline", offline);
    window.addEventListener("online", online);
    return () => {
      closed = true;
      es.close();
      if (timer) clearTimeout(timer);
      if (retry) clearTimeout(retry);
      window.removeEventListener("offline", offline);
      window.removeEventListener("online", online);
    };
  }, [enabled, qc]);
}
