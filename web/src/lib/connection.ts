import { useSyncExternalStore } from "react";

/**
 * Whether mangarr answers. While it restarts (an update, a settings change)
 * requests fail for a few seconds: pages keep what they show and retry
 * quietly, a small "Reconnecting" note shows, and everything that was
 * waiting picks up again once /ping answers.
 */
let down = false;
const listeners = new Set<() => void>();
const backListeners = new Set<() => void>();
let probe: ReturnType<typeof setTimeout> | undefined;
let delay = 1000;
let pingUrl = "/ping";

/** setPingUrl points the probe at this server's /ping (under its URL base). */
export function setPingUrl(url: string) {
  pingUrl = url;
}

/** unreachable reports an error that means the server isn't there right now. */
export function unreachable(error: unknown): boolean {
  if ((error as { unreachable?: boolean } | null)?.unreachable) return true;
  // what fetch throws when nothing answers: "Failed to fetch" (Chrome),
  // "NetworkError when attempting to fetch resource." (Firefox), "Load failed" (Safari)
  return error instanceof TypeError && /fetch|network|load failed/i.test(error.message);
}

function emit() {
  listeners.forEach((l) => l());
}

function schedule() {
  if (probe !== undefined) return;
  probe = setTimeout(async () => {
    probe = undefined;
    if (await ping()) return serverAnswered();
    serverGone();
  }, delay);
  delay = Math.min(delay * 2, 10_000);
}

async function ping() {
  try {
    return (await fetch(pingUrl, { cache: "no-store" })).ok;
  } catch {
    return false;
  }
}

/** serverGone records a failed request; the server is probed until it answers. */
export function serverGone() {
  if (!down) {
    down = true;
    emit();
  }
  schedule();
}

/** OFFLINE_HEADER marks an answer the service worker made itself from what it saved (public/sw.js). */
export const OFFLINE_HEADER = "x-mangarr-offline";

/** fromServer: the answer came from mangarr, not from the service worker standing in for it. */
export const fromServer = (r: Response) => !r.headers.has(OFFLINE_HEADER);

/** serverAnswered records any answer from the server. */
export function serverAnswered() {
  if (!down) return;
  down = false;
  delay = 1000;
  if (probe !== undefined) clearTimeout(probe);
  probe = undefined;
  emit();
  backListeners.forEach((l) => l());
}

/** checkServer asks /ping once (a dropped stream may be a blip, or a restart). */
export async function checkServer() {
  if (down) return;
  if (!(await ping())) serverGone();
}

/** onServerBack runs fn each time the server answers again after being gone. */
export function onServerBack(fn: () => void) {
  backListeners.add(fn);
  return () => {
    backListeners.delete(fn);
  };
}

export function isServerDown() {
  return down;
}

/** useServerDown is true while mangarr isn't answering. */
export function useServerDown() {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => down,
    () => false,
  );
}

/** reset is for tests. */
export function resetConnection() {
  if (probe !== undefined) clearTimeout(probe);
  probe = undefined;
  down = false;
  delay = 1000;
  listeners.clear();
  backListeners.clear();
}
