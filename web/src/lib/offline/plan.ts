/** What to save on this device, kept apart from the storage itself so it can be tested on its own. */

/** ahead: saved by the reader while reading, removed once read; kept: saved on purpose. */
export type SaveKind = "ahead" | "kept";

export type SavedChapter = {
  id: number;
  seriesId: number;
  seriesTitle: string;
  number: string;
  title?: string;
  kind: SaveKind;
  pages: number;
  /** Pages stored so far; the chapter is readable offline once it equals pages. */
  saved: number;
  bytes: number;
  width: number;
  at: number;
  /** When it was read to the end on this device. */
  readAt?: number;
  /** chapterVersion of the details it was saved from, to notice the file changing on the server. */
  version?: string;
};

export type Index = Record<string, SavedChapter>;

/** Widths the server keeps page copies at (internal/imagedeliver). */
export const pageWidths = [320, 720, 1080, 1440, 2160];

/** saveWidth is the page width to save at: what the reader asks for on this screen, so saved pages stand in for it. */
export function saveWidth(widths: readonly number[], cssWidth: number, ratio = window.devicePixelRatio || 1) {
  const want = Math.round(cssWidth * ratio);
  return widths.find((w) => want <= w) ?? widths[widths.length - 1];
}

/** readKept is the chapters saved on purpose for a series that have been read, other than the open one. */
export function readKept(saved: Index, seriesId: number, open: number): number[] {
  return Object.values(saved)
    .filter((c) => c.kind === "kept" && c.seriesId === seriesId && c.readAt && c.id !== open)
    .map((c) => c.id);
}

/** estimateBytes guesses what saving `chapters` more will take, from what's saved already (about 12 MB a chapter otherwise). */
export function estimateBytes(saved: Index, chapters: number) {
  const done = Object.values(saved).filter((c) => c.pages > 0 && c.saved >= c.pages);
  const each = done.length ? done.reduce((n, c) => n + c.bytes, 0) / done.length : 12e6;
  return Math.round(each * chapters);
}

/** staleAhead is the chapters saved ahead for a series that the reader no longer keeps (`keep`: the open one and the next few). */
export function staleAhead(saved: Index, seriesId: number, keep: ReadonlySet<number>): number[] {
  return Object.values(saved)
    .filter((c) => c.kind === "ahead" && c.seriesId === seriesId && !keep.has(c.id))
    .map((c) => c.id);
}

/** unfinished is the chapters saved on purpose that stopped part way (the page was closed or reloaded) and aren't waiting in `queued`, oldest first. */
export function unfinished(saved: Index, queued: ReadonlySet<number>): SavedChapter[] {
  return Object.values(saved)
    .filter((c) => c.kind === "kept" && c.saved < c.pages && !queued.has(c.id))
    .sort((a, b) => a.at - b.at);
}

/** nearestSaved is the closest complete saved chapter of the same series after (or before) `from`, by chapter number. */
export function nearestSaved(saved: Index, from: { id: number; seriesId: number; number: string }, dir: "next" | "prev") {
  const n = Number(from.number);
  const list = Object.values(saved)
    .filter((c) => c.seriesId === from.seriesId && c.id !== from.id && c.pages > 0 && c.saved >= c.pages)
    .filter((c) => (dir === "next" ? Number(c.number) > n : Number(c.number) < n))
    .sort((a, b) => (dir === "next" ? 1 : -1) * (Number(a.number) - Number(b.number)));
  return list[0] ? { id: list[0].id, number: list[0].number } : undefined;
}

/**
 * chapterVersion sums up a chapter's pages (count, types and sizes): it
 * changes when the server's file does (a better release, upscaled or
 * re-encoded pages), so a copy saved before is known to be out of date.
 */
export function chapterVersion(pages: readonly { number: number; mediaType: string; size?: number }[]) {
  let h = 0x811c9dc5;
  for (const p of pages) {
    for (const ch of `${p.number}:${p.mediaType}:${p.size ?? ""};`) {
      h ^= ch.charCodeAt(0);
      h = Math.imul(h, 0x01000193);
    }
  }
  return `${pages.length}-${(h >>> 0).toString(36)}`;
}

/** readSince is the chapters of a title saved ahead that come before the next one to read. */
export function readSince(saved: Index, seriesId: number, next: string) {
  const n = Number(next);
  if (!next || !Number.isFinite(n)) return [];
  return Object.values(saved)
    .filter((c) => c.kind === "ahead" && c.seriesId === seriesId && Number(c.number) < n)
    .map((c) => c.id);
}
