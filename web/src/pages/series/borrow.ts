import type { Chapter, S } from "../../api/client";

export type Borrowed = { chapter: Chapter; edition: S["EditionSummary"] };

/** borrowChapters picks the downloaded chapters this edition has no file
 * for from the other editions, the first edition listed winning, newest
 * first. */
export function borrowChapters(own: Chapter[], others: { edition: S["EditionSummary"]; chapters: Chapter[] }[]): Borrowed[] {
  const have = new Set(own.filter((c) => c.file).map((c) => c.numberSort));
  const out = new Map<number, Borrowed>();
  for (const { edition, chapters } of others)
    for (const chapter of chapters) if (chapter.file && !have.has(chapter.numberSort) && !out.has(chapter.numberSort)) out.set(chapter.numberSort, { chapter, edition });
  return [...out.values()].sort((a, b) => b.chapter.numberSort - a.chapter.numberSort);
}
