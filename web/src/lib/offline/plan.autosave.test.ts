import { describe, expect, test } from "vitest";
import { chapterVersion, readSince, type SavedChapter } from "./plan";

const ch = (id: number, number: string, kind: SavedChapter["kind"], seriesId = 1): SavedChapter => ({
  id, seriesId, seriesTitle: "", number, kind, pages: 10, saved: 10, bytes: 0, width: 1080, at: 0,
});

describe("auto-save", () => {
  test("drops chapters saved ahead that were read since, never ones saved on purpose or of other titles", () => {
    const saved = { 1: ch(1, "1", "ahead"), 2: ch(2, "2", "kept"), 3: ch(3, "3", "ahead"), 4: ch(4, "4", "ahead"), 9: ch(9, "1", "ahead", 2) };
    expect(readSince(saved, 1, "3").sort()).toEqual([1]);
    expect(readSince(saved, 1, "")).toEqual([]);
  });

  test("a chapter's version changes when its file does", () => {
    const pages = [{ number: 1, mediaType: "image/jpeg", size: 1000 }, { number: 2, mediaType: "image/jpeg", size: 2000 }];
    expect(chapterVersion(pages)).toBe(chapterVersion(pages.map((p) => ({ ...p }))));
    expect(chapterVersion(pages)).not.toBe(chapterVersion([pages[0], { ...pages[1], size: 1500 }]));
    expect(chapterVersion(pages)).not.toBe(chapterVersion([pages[0], { ...pages[1], mediaType: "image/avif" }]));
    expect(chapterVersion(pages)).not.toBe(chapterVersion(pages.slice(0, 1)));
  });
});
