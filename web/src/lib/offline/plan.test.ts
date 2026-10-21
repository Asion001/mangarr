import { describe, expect, test } from "vitest";
import { nearestSaved, saveWidth, staleAhead, type SavedChapter } from "./plan";

const widths = [320, 720, 1080, 1440, 2160];
const ch = (id: number, kind: SavedChapter["kind"], seriesId = 1): SavedChapter => ({
  id, seriesId, seriesTitle: "", number: String(id), kind, pages: 10, saved: 10, bytes: 0, width: 1080, at: 0,
});

describe("saved chapters", () => {
  test("saves at the server width that covers the screen", () => {
    expect(saveWidth(widths, 390, 3)).toBe(1440);
    expect(saveWidth(widths, 360, 2)).toBe(720);
    expect(saveWidth(widths, 1600, 2)).toBe(2160);
  });

  test("drops chapters saved ahead that are no longer ahead, never ones saved on purpose", () => {
    const saved = { 1: ch(1, "ahead"), 2: ch(2, "ahead"), 3: ch(3, "ahead"), 4: ch(4, "kept"), 9: ch(9, "ahead", 2) };
    expect(staleAhead(saved, 1, new Set([2, 3])).sort()).toEqual([1]);
    expect(staleAhead(saved, 1, new Set()).sort()).toEqual([1, 2, 3]);
  });

  test("offline, next and previous go to the nearest saved chapter", () => {
    const saved = { 3: ch(3, "ahead"), 5: ch(5, "kept"), 6: { ...ch(6, "ahead"), saved: 4 }, 1: ch(1, "kept") };
    expect(nearestSaved(saved, { id: 2, seriesId: 1, number: "2" }, "next")?.id).toBe(3);
    expect(nearestSaved(saved, { id: 4, seriesId: 1, number: "4" }, "next")?.id).toBe(5);
    expect(nearestSaved(saved, { id: 5, seriesId: 1, number: "5" }, "next")).toBeUndefined();
    expect(nearestSaved(saved, { id: 3, seriesId: 1, number: "3" }, "prev")?.id).toBe(1);
  });
});
