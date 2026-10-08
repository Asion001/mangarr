import type { Chapter, S } from "../../api/client";

export type Borrowed = { chapter: Chapter; edition: S["EditionSummary"] };

/** borrowChapters picks the downloaded chapters of the other editions that
 * this edition doesn't list at all, the first edition listed winning,
 * newest first. A chapter this edition lists but hasn't downloaded yet is
 * its own to come, not "only in another language". */
export function borrowChapters(own: Chapter[], others: { edition: S["EditionSummary"]; chapters: Chapter[] }[]): Borrowed[] {
  const have = new Set(own.map((c) => c.numberSort));
  const out = new Map<number, Borrowed>();
  for (const { edition, chapters } of others)
    for (const chapter of chapters) if (chapter.file && !have.has(chapter.numberSort) && !out.has(chapter.numberSort)) out.set(chapter.numberSort, { chapter, edition });
  return [...out.values()].sort((a, b) => b.chapter.numberSort - a.chapter.numberSort);
}
