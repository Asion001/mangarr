import { describe, expect, it } from "vitest";
import { isStaleChunk } from "./RouteBoundary";

describe("isStaleChunk", () => {
  it("recognises a page file that is gone after an update", () => {
    expect(isStaleChunk(new TypeError("Failed to fetch dynamically imported module: http://x/assets/Tasks-abc.js"))).toBe(true);
    expect(isStaleChunk(new TypeError("Importing a module script failed."))).toBe(true);
    expect(isStaleChunk(new Error("error loading dynamically imported module"))).toBe(true);
  });
  it("leaves ordinary errors alone", () => {
    expect(isStaleChunk(new Error("Cannot read properties of undefined"))).toBe(false);
  });
});
