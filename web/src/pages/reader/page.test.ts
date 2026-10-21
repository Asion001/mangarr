import { describe, expect, it } from "vitest";
import type { Dims } from "./page";

// page.tsx pulls in the API client, which reads the page's address, and the offline store, which listens for storage events
globalThis.document ??= { baseURI: "http://localhost/" } as Document;
globalThis.window ??= { location: new URL("http://localhost/"), addEventListener: () => undefined } as unknown as Window & typeof globalThis;
const { imageWidth } = await import("./page");

const spread: Dims = { width: 2000, height: 1400, x: 100, y: 50, w: 1800, h: 1300 };

describe("imageWidth", () => {
  it("is the shown width for a whole page", () => {
    expect(imageWidth(400, spread, false)).toBe(400);
  });
  it("counts the half that is not shown", () => {
    expect(imageWidth(400, spread, false, false, "left")).toBe(800);
  });
  it("counts the borders cropped away", () => {
    expect(imageWidth(360, spread, true)).toBe(400);
  });
  it("falls back to the shown width before the page is measured", () => {
    expect(imageWidth(400, undefined, true, false, "right")).toBe(400);
  });
});
