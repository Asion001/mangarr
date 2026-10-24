import { useSyncExternalStore } from "react";
import { apiUrl, type S } from "../../api/client";
import { pageWidths, saveWidth, unfinished, type Index, type SaveKind, type SavedChapter } from "./plan";

export { estimateBytes, nearestSaved, pageWidths, readKept, saveWidth, staleAhead, type SaveKind, type SavedChapter } from "./plan";

/**
 * Chapters saved on this device, for reading through a server restart or
 * with no network. Each chapter lives in its own Cache Storage cache (its
 * details, bounds and pages, under the URLs the reader asks for), which the
 * service worker (public/sw.js) answers from. The list of them, with what
 * the pages UI shows, is kept in localStorage.
 */

type Chapter = S["ReadChapter"];

/** Must match public/sw.js. */
const CHAPTER_PREFIX = "mangarr-chapter-";
const WIDTH_HEADER = "x-mangarr-width";
const INDEX_KEY = "mangarr.offline.v1";
const REMOVE_READ_KEY = "mangarr.offline.removeRead";
/** Pages fetched at once while saving a chapter. */
const PARALLEL = 3;


function readIndex(): Index {
  try {
    return JSON.parse(localStorage.getItem(INDEX_KEY) || "{}") as Index;
  } catch {
    return {};
  }
}

let index: Index = readIndex();
const listeners = new Set<() => void>();

function writeIndex(next: Index) {
  index = next;
  try {
    localStorage.setItem(INDEX_KEY, JSON.stringify(next));
  } catch {
    // a full or blocked localStorage only loses the list, not the pages
  }
  for (const l of listeners) l();
}

function patch(id: number, change: Partial<SavedChapter> | null) {
  const next = { ...index };
  if (change === null) delete next[id];
  else next[id] = { ...next[id], ...change } as SavedChapter;
  writeIndex(next);
}

/** subscribe calls back whenever the saved chapters change (in this tab or another). */
export function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
if (typeof window !== "undefined") {
  window.addEventListener("storage", (e) => {
    if (e.key !== INDEX_KEY) return;
    index = readIndex();
    for (const l of listeners) l();
  });
}

/** savedChapters is every chapter on this device, complete or still saving. */
export const savedChapters = (): Index => index;

/** useSavedChapters is savedChapters, kept up to date. */
export const useSavedChapters = () => useSyncExternalStore(subscribe, savedChapters);

/** markRead notes that a saved chapter was read to the end, for "remove chapters I saved once I've read them". */
export function markRead(id: number) {
  if (index[id] && !index[id].readAt) patch(id, { readAt: Date.now() });
}

/** removeRead: chapters saved on purpose are removed once read. */
export function removeRead() {
  try {
    return localStorage.getItem(REMOVE_READ_KEY) === "1";
  } catch {
    return false;
  }
}
export const useRemoveRead = () => useSyncExternalStore(subscribe, removeRead);
export function setRemoveRead(on: boolean) {
  try {
    localStorage.setItem(REMOVE_READ_KEY, on ? "1" : "0");
  } catch {
    // the choice just isn't remembered
  }
  for (const l of listeners) l();
}

/** isSaved: every page of the chapter is on this device. */
export function isSaved(id: number) {
  const c = index[id];
  return !!c && c.pages > 0 && c.saved >= c.pages;
}

/** offlineReady: a service worker answers this page's requests, so saved chapters can be read from. */
export const offlineReady = () => typeof navigator !== "undefined" && !!navigator.serviceWorker?.controller && typeof caches !== "undefined";

const url = (path: string, query?: Record<string, string | number>) => new URL(apiUrl(path, query), location.href).href;

class Quota extends Error {}

async function put(cache: Cache, key: string, body: Blob, type: string, width?: number) {
  const headers: Record<string, string> = { "Content-Type": type };
  if (width !== undefined) headers[WIDTH_HEADER] = String(width);
  try {
    await cache.put(key, new Response(body, { headers }));
  } catch (err) {
    if (err instanceof DOMException && err.name === "QuotaExceededError") throw new Quota();
    throw err;
  }
}

async function fetchBlob(u: string, signal?: AbortSignal) {
  const r = await fetch(u, { signal, credentials: "same-origin" });
  if (!r.ok) throw new Error(`${r.status}`);
  return { blob: await r.blob(), type: r.headers.get("Content-Type") || "application/octet-stream" };
}

const running = new Map<number, Promise<void>>();

/**
 * saveChapter stores a chapter's details, bounds and every page at `width`.
 * It picks up where an earlier save stopped; saving a chapter that is
 * already saved only upgrades it from "ahead" to "kept".
 */
export function saveChapter(ch: Chapter, kind: SaveKind, width: number, signal?: AbortSignal): Promise<void> {
  const had = index[ch.id];
  if (had && kind === "kept" && had.kind !== "kept") patch(ch.id, { kind });
  if (isSaved(ch.id)) return Promise.resolve();
  // a chapter half saved at another width carries on at that width
  if (had) width = had.width;
  // one save per chapter at a time; a stopped one is picked up where it left off
  const busy = running.get(ch.id);
  if (busy) return busy.catch(() => undefined).then(() => saveChapter(ch, kind, width, signal));
  const job = (async () => {
    // ask the browser not to clear saved chapters under storage pressure
    void navigator.storage?.persist?.().catch(() => false);
    const cache = await caches.open(CHAPTER_PREFIX + ch.id);
    patch(ch.id, {
      id: ch.id,
      seriesId: ch.seriesId,
      seriesTitle: ch.seriesTitle,
      number: ch.number,
      title: ch.title,
      kind: had?.kind === "kept" ? "kept" : kind,
      pages: ch.pages.length,
      saved: had?.saved ?? 0,
      bytes: had?.bytes ?? 0,
      width,
      at: Date.now(),
    });
    // the details without a CDN link, so the reader asks for pages the service worker has
    await put(cache, url(`api/v1/read/chapters/${ch.id}`), new Blob([JSON.stringify({ ...ch, pageBase: undefined })]), "application/json");
    try {
      const b = await fetchBlob(url(`api/v1/read/chapters/${ch.id}/bounds`), signal);
      await put(cache, url(`api/v1/read/chapters/${ch.id}/bounds`), b.blob, b.type);
    } catch (err) {
      if (err instanceof Quota) throw err;
      // bounds only matter for cropping; the reader measures pages itself without them
    }
    const have = new Set((await cache.keys()).map((r) => new URL(r.url).pathname));
    const todo = ch.pages.map((p) => p.number).filter((n) => !have.has(new URL(url(`api/v1/read/chapters/${ch.id}/pages/${n}`)).pathname));
    let saved = ch.pages.length - todo.length;
    let bytes = index[ch.id]?.bytes ?? 0;
    const worker = async () => {
      for (let n = todo.shift(); n !== undefined; n = todo.shift()) {
        if (signal?.aborted || !index[ch.id]) return;
        const key = url(`api/v1/read/chapters/${ch.id}/pages/${n}`);
        const b = await fetchBlob(url(`api/v1/read/chapters/${ch.id}/pages/${n}`, { w: width }), signal);
        await put(cache, key, b.blob, b.type, width);
        saved++;
        bytes += b.blob.size;
        if (index[ch.id]) patch(ch.id, { saved, bytes });
      }
    };
    await Promise.all(Array.from({ length: PARALLEL }, worker));
  })().finally(() => running.delete(ch.id));
  running.set(ch.id, job);
  return job;
}

/** deviceWidth is the width to save at outside the reader: a page filling this screen upright. */
export const deviceWidth = () => saveWidth(pageWidths, Math.min(window.innerWidth, window.innerHeight * 0.75));

/** saveChapterById saves a chapter on purpose, fetching its details first. */
export async function saveChapterById(id: number, width = deviceWidth()) {
  const r = await fetch(url(`api/v1/read/chapters/${id}`), { credentials: "same-origin" });
  if (!r.ok) throw new Error(`${r.status}`);
  await saveChapter((await r.json()) as Chapter, "kept", width);
}

/** A chapter waiting its turn to be saved on purpose. */
export type QueuedChapter = { id: number; seriesId: number; seriesTitle?: string; number: string };

const QUEUE_KEY = "mangarr.offline.queue";

function readQueue(): QueuedChapter[] {
  try {
    return JSON.parse(localStorage.getItem(QUEUE_KEY) || "[]") as QueuedChapter[];
  } catch {
    return [];
  }
}

let queue: QueuedChapter[] = readQueue();

function writeQueue(next: QueuedChapter[]) {
  queue = next;
  try {
    localStorage.setItem(QUEUE_KEY, JSON.stringify(next));
  } catch {
    // the queue then only lasts until the page closes
  }
  for (const l of listeners) l();
}

/** saveQueue is the chapters waiting to be saved, in order. */
export const saveQueue = (): QueuedChapter[] => queue;

/** useSaveQueue is saveQueue, kept up to date. */
export const useSaveQueue = () => useSyncExternalStore(subscribe, saveQueue);

let draining = false;
let retry: ReturnType<typeof setTimeout> | undefined;
/** How long a queue that stopped on an error waits before trying again. */
const RETRY_MS = 30_000;

/** drain saves the queued chapters one at a time; a chapter that fails stays queued for the next try. */
async function drain() {
  if (draining || !offlineReady()) return;
  draining = true;
  clearTimeout(retry);
  try {
    // a chapter saved on its own that the page closed on goes first, then the queue
    const stopped = unfinished(index, new Set(queue.map((q) => q.id))).filter((c) => !running.has(c.id));
    if (stopped.length) writeQueue([...stopped.map(({ id, seriesId, seriesTitle, number }) => ({ id, seriesId, seriesTitle, number })), ...queue]);
    for (let next = queue[0]; next; next = queue[0]) {
      try {
        await saveChapterById(next.id);
      } catch {
        // no server, or out of space: try again later
        retry = setTimeout(() => void drain(), RETRY_MS);
        return;
      }
      writeQueue(queue.filter((q) => q.id !== next.id));
    }
  } finally {
    draining = false;
  }
}

/**
 * queueSave adds chapters to the save queue and starts saving them. Chapters
 * already saved or already waiting are skipped; it returns how many it added.
 */
export function queueSave(chapters: QueuedChapter[]) {
  const waiting = new Set(queue.map((q) => q.id));
  const add = chapters.filter((c) => !waiting.has(c.id) && !isSaved(c.id));
  if (add.length) writeQueue([...queue, ...add]);
  void drain();
  return add.length;
}

/** unqueue takes chapters out of the save queue, dropping what a started one had saved so it isn't picked up again. */
export function unqueue(ids: number[]) {
  const drop = new Set(ids);
  writeQueue(queue.filter((q) => !drop.has(q.id)));
  for (const id of ids) if (saving(index[id])) void removeChapter(id);
}

if (typeof window !== "undefined") {
  window.addEventListener("storage", (e) => {
    if (e.key === QUEUE_KEY) {
      queue = readQueue();
      for (const l of listeners) l();
    }
  });
  // what was still waiting or saving when the page closed carries on, once a service worker answers
  window.addEventListener("online", () => void drain());
  navigator.serviceWorker?.addEventListener("controllerchange", () => void drain());
  setTimeout(() => void drain(), 2000);
}

/** removeChapter deletes a saved chapter from this device. */
export async function removeChapter(id: number) {
  patch(id, null);
  await caches.delete(CHAPTER_PREFIX + id).catch(() => false);
}

/** removeAll deletes every saved chapter from this device. */
export async function removeAll() {
  const ids = Object.keys(index).map(Number);
  writeQueue([]);
  writeIndex({});
  await Promise.all(ids.map((id) => caches.delete(CHAPTER_PREFIX + id).catch(() => false)));
}

/** savingNow: a save of the chapter is running in this tab (a partial one that isn't was stopped). */
export const savingNow = (id: number) => running.has(id);

/** saving reports a chapter that has started saving but isn't complete. */
export const saving = (c: SavedChapter | undefined) => !!c && c.saved < c.pages;
