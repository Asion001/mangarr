import { describe, expect, it } from "vitest";
import { packsOf } from "./credits";

const track = (id: number, pack: string, title: string, license: string) =>
  ({ id, pack, title, license, attribution: "", sourceUrl: "", kind: "ambience" }) as Parameters<typeof packsOf>[0][number];

describe("packsOf", () => {
  it("groups sounds by pack, sorted, with each pack's licenses", () => {
    const got = packsOf([track(1, "Weather", "Wind", "CC0"), track(2, "Effects", "Thunder clap", "CC BY 4.0"), track(3, "Weather", "Rain", "CC0"), track(4, "Effects", "Door", "CC0")]);
    expect(got.map((p) => p.name)).toEqual(["Effects", "Weather"]);
    expect(got[0].licenses).toEqual(["CC BY 4.0", "CC0"]);
    expect(got[1].sounds.map((s) => s.title)).toEqual(["Rain", "Wind"]);
  });
});
