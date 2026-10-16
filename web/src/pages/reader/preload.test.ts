import { describe, expect, test } from "vitest";
import { ImagePreloader, widen } from "./preload";

class FakeImage {
  src = "";
  decoding = "auto";
  cancelled = false;
  removeAttribute(name: string) {
    if (name === "src") {
      this.src = "";
      this.cancelled = true;
    }
  }
}

describe("reader image preloading", () => {
  test("keeps useful requests and cancels pages skipped by a fast jump", () => {
    const made: FakeImage[] = [];
    const preloader = new ImagePreloader(() => {
      const image = new FakeImage();
      made.push(image);
      return image as unknown as HTMLImageElement;
    });

    preloader.update(["page-2", "page-3", "page-4"]);
    expect(made.map((image) => image.src)).toEqual(["page-2", "page-3", "page-4"]);

    preloader.update(["page-4", "page-10", "page-11"], ["page-3"]);
    expect(made[0].cancelled).toBe(true);
    expect(made[1].cancelled).toBe(false);
    expect(made[2].cancelled).toBe(false);
    expect(made.slice(3).map((image) => image.src)).toEqual(["page-10", "page-11"]);

    preloader.clear();
    expect(made.slice(1).every((image) => image.cancelled)).toBe(true);
  });
});

describe("webtoon load window", () => {
  test("adds the pages ahead and keeps the same set when nothing changes", () => {
    const start = new Set([1, 2]);
    const wider = widen(start, 1, 5, 4);
    expect([...wider].sort()).toEqual([1, 2, 3, 4]);
    expect(widen(wider, 0, 3, 4)).toBe(wider);
  });
});
