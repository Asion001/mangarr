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
