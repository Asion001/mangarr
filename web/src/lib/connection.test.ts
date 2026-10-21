import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isServerDown, onServerBack, resetConnection, serverAnswered, serverGone, unreachable } from "./connection";

describe("unreachable", () => {
  it("knows a restart from an ordinary error", () => {
    expect(unreachable(new TypeError("Failed to fetch"))).toBe(true);
    expect(unreachable(new TypeError("NetworkError when attempting to fetch resource."))).toBe(true);
    expect(unreachable(new TypeError("Load failed"))).toBe(true);
    expect(unreachable({ status: 502, unreachable: true })).toBe(true);
    expect(unreachable({ status: 503, unreachable: false })).toBe(false);
    expect(unreachable({ status: 404 })).toBe(false);
    expect(unreachable(new TypeError("x is undefined"))).toBe(false);
  });
});

describe("server connection", () => {
  beforeEach(() => {
    resetConnection();
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("probes until mangarr answers, then tells whoever waits", async () => {
    let up = false;
    const fetch = vi.fn(async () => {
      if (!up) throw new TypeError("Failed to fetch");
      return new Response("pong");
    });
    vi.stubGlobal("fetch", fetch);
    const back = vi.fn();
    onServerBack(back);

    serverGone();
    expect(isServerDown()).toBe(true);
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(back).not.toHaveBeenCalled();

    up = true;
    await vi.advanceTimersByTimeAsync(2000);
    expect(isServerDown()).toBe(false);
    expect(back).toHaveBeenCalledTimes(1);

    // an answer while nothing was down changes nothing
    serverAnswered();
    expect(back).toHaveBeenCalledTimes(1);
  });
});
