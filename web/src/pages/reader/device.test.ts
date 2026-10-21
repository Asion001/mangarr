import { describe, expect, it } from "vitest";
import { readDevice, seedDevice, writeDevice } from "./device";

describe("seedDevice", () => {
  it("copies the account's settings onto a new device", () => {
    const d = seedDevice({ series: {} }, 7, { defaults: { crop: true }, series: { mode: "webtoon" } });
    expect(d).toEqual({ defaults: { crop: true }, series: { 7: { mode: "webtoon" } } });
  });

  it("keeps what the device already has", () => {
    const d = seedDevice({ defaults: { crop: false }, series: { 7: null } }, 7, { defaults: { crop: true }, series: { mode: "webtoon" } });
    expect(d).toEqual({ defaults: { crop: false }, series: { 7: null } });
  });

  it("adds a series the device hasn't opened yet, without touching its defaults", () => {
    const d = seedDevice({ defaults: { gap: true }, series: { 1: { padding: 5 } } }, 2, { defaults: { gap: false }, series: null });
    expect(d).toEqual({ defaults: { gap: true }, series: { 1: { padding: 5 }, 2: null } });
  });
});

describe("a browser that keeps no site data", () => {
  it("keeps what was written until the page closes", () => {
    const blocked = () => {
      throw new DOMException("blocked", "SecurityError");
    };
    globalThis.localStorage = { getItem: blocked, setItem: blocked } as unknown as Storage;
    writeDevice("k", { defaults: {}, series: { 3: { crop: true } } });
    expect(readDevice("k")).toEqual({ defaults: {}, series: { 3: { crop: true } } });
  });
});
