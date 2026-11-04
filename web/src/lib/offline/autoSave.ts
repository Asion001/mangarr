import { useSyncExternalStore } from "react";
import { apiUrl, type S } from "../../api/client";
import { deviceWidth, offlineReady, removeChapter, saveChapter, savedChapters } from "./store";
import { readSince } from "./plan";

/**
 * Auto-save keeps the next unread chapters of the titles you're reading on
 * this device, like a reading app's auto-download: when the app opens or
 * comes back to the foreground, the next few chapters of each title on the
 * Continue reading shelf are saved, so they open without the server or a
 * network. They're saved "ahead", so they go once read.
 *
 * iOS has no background downloads, so this only runs while the app is open.
 */

const KEY = "mangarr.offline.autoSave";
/** Titles from the Continue reading shelf it looks at, most recently read first. */
const TITLES = 10;
/** How often coming back to the app looks again. */
const EVERY_MS = 15 * 60_000;
/** Marks an answer the service worker made itself (public/sw.js): the server wasn't asked. */
const OFFLINE_HEADER = "x-mangarr-offline";

const listeners = new Set<() => void>();

/** autoSaveCount is how many unread chapters ahead to keep for each title (0: off). */
export function autoSaveCount() {
  try {
    return Math.max(0, Number(localStorage.getItem(KEY) || 0) || 0);
  } catch {
    return 0;
  }
}
const subscribeCount = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};
export const useAutoSaveCount = () => useSyncExternalStore(subscribeCount, autoSaveCount);
export function setAutoSaveCount(n: number) {
  try {
    localStorage.setItem(KEY, String(n));
  } catch {
    // the choice just isn't remembered
  }
  for (const l of listeners) l();
  if (n > 0) void autoSave(true);
}

/** metered: the browser says this is a mobile or data-saving connection (only some browsers can tell; iOS can't). */
function metered() {
  const c = (navigator as Navigator & { connection?: { type?: string; saveData?: boolean } }).connection;
  return !!c && (c.type === "cellular" || !!c.saveData);
}

async function getJSON<T>(path: string, query?: Record<string, string | number>): Promise<T | undefined> {
  const r = await fetch(apiUrl(path, query), { credentials: "same-origin" });
  // signed out, or the server is away and this is a kept copy: nothing to save from
  if (!r.ok || r.headers.get(OFFLINE_HEADER)) return undefined;
  return (await r.json()) as T;
}

let running = false;
let last = 0;

/**
 * autoSave saves the next unread chapters of each title being read, and
 * drops chapters saved ahead that have been read since. Without `now` it
 * runs at most every EVERY_MS.
 */
export async function autoSave(now = false) {
  const count = autoSaveCount();
  if (!count || running || !offlineReady() || metered()) return;
  if (!now && Date.now() - last < EVERY_MS) return;
  running = true;
  last = Date.now();
  try {
    const shelf = await getJSON<S["Shelf"]>("api/v1/reading/shelf", { limit: TITLES });
    for (const item of shelf?.items ?? []) {
      // read since (here or on another device): no longer needed
      for (const id of readSince(savedChapters(), item.seriesId, item.next.number)) await removeChapter(id);
      let id = item.next.available ? item.next.chapterId : 0;
      for (let i = 0; i < count && id; i++) {
        const ch = await getJSON<S["ReadChapter"]>(`api/v1/read/chapters/${id}`);
        // only chapters with a file: streaming from a source isn't worth it unattended
        if (!ch || !ch.downloaded) break;
        await saveChapter(ch, "ahead", deviceWidth());
        id = ch.next?.id ?? 0;
      }
    }
  } catch {
    // no server, or out of space: the next chance tries again
    last = 0;
  } finally {
    running = false;
  }
}

if (typeof window !== "undefined" && typeof document !== "undefined") {
  setTimeout(() => void autoSave(), 5000);
  window.addEventListener("online", () => void autoSave(true));
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") void autoSave();
  });
  navigator.serviceWorker?.addEventListener("controllerchange", () => void autoSave());
}
