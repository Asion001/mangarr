// Soundtrack choices kept on this device: whether music plays (off until a
// reader turns it on), how it follows the story, its volume and where it
// plays. Nothing here talks to the server.
import { useSyncExternalStore } from "react";
import type { S } from "../../api/client";

export type Mood = S["AudioMood"];
export type Resolved = S["Resolved"];

/** auto follows the title's soundtrack; manual keeps the mood picked by hand. */
export type SoundMode = "auto" | "manual" | "off";

export type SoundPrefs = {
  mode: SoundMode;
  /** the mode the speaker button turns back on */
  lastMode: Exclude<SoundMode, "off">;
  /** the mood picked by hand (manual mode) */
  moodId: number | null;
  /** music volume, 0-100 */
  volume: number;
  /** the Spotify device music plays on ("" = this browser, or the active one) */
  deviceId: string;
};

export const defaultPrefs: SoundPrefs = { mode: "off", lastMode: "auto", moodId: null, volume: 70, deviceId: "" };

const key = "mangarr:soundtrack";

/** parsePrefs reads stored choices, keeping only what makes sense. */
export function parsePrefs(raw: string | null): SoundPrefs {
  let v: Partial<SoundPrefs> = {};
  try {
    const parsed: unknown = raw ? JSON.parse(raw) : {};
    if (parsed && typeof parsed === "object") v = parsed as Partial<SoundPrefs>;
  } catch {
    /* unreadable: defaults */
  }
  const modes: SoundMode[] = ["auto", "manual", "off"];
  return {
    mode: modes.includes(v.mode as SoundMode) ? (v.mode as SoundMode) : defaultPrefs.mode,
    lastMode: v.lastMode === "manual" ? "manual" : "auto",
    moodId: typeof v.moodId === "number" && v.moodId > 0 ? v.moodId : null,
    volume: clampVolume(typeof v.volume === "number" ? v.volume : defaultPrefs.volume),
    deviceId: typeof v.deviceId === "string" ? v.deviceId : "",
  };
}

export const clampVolume = (v: number) => (Number.isFinite(v) ? Math.round(Math.min(100, Math.max(0, v))) : defaultPrefs.volume);

/** withMode switches the mode, remembering the last one that plays. */
export function withMode(p: SoundPrefs, mode: SoundMode): SoundPrefs {
  return { ...p, mode, lastMode: mode === "off" ? p.lastMode : mode };
}

/** turnOn is what the speaker button does when music is off. */
export const turnOn = (p: SoundPrefs): SoundPrefs => withMode(p, p.lastMode);

let current: SoundPrefs | null = null;
const listeners = new Set<() => void>();

export function getPrefs(): SoundPrefs {
  if (!current) {
    let raw: string | null = null;
    try {
      raw = localStorage.getItem(key);
    } catch {
      /* no storage */
    }
    current = parsePrefs(raw);
  }
  return current;
}

export function setPrefs(update: Partial<SoundPrefs> | ((p: SoundPrefs) => SoundPrefs)) {
  const prev = getPrefs();
  current = typeof update === "function" ? update(prev) : { ...prev, ...update };
  try {
    localStorage.setItem(key, JSON.stringify(current));
  } catch {
    /* no storage: kept for this visit */
  }
  listeners.forEach((fn) => fn());
}

const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
};

/** useSoundPrefs is this device's soundtrack choices. */
export function useSoundPrefs(): SoundPrefs {
  return useSyncExternalStore(subscribe, getPrefs, getPrefs);
}

/**
 * isPhone reports a phone or tablet browser, where Spotify's Web Playback
 * SDK doesn't run: music plays on the reader's Spotify app instead.
 */
export function isPhone(ua: string, maxTouchPoints = 0): boolean {
  if (/Android|iPhone|iPad|iPod|Mobile|Silk|Kindle/i.test(ua)) return true;
  // iPadOS asks for desktop sites
  return /Macintosh/.test(ua) && maxTouchPoints > 1;
}

/**
 * currentMood is what should play: in auto mode the title's soundtrack (a
 * page cue once the reader reaches it), in manual mode the mood picked.
 */
export function currentMood(prefs: SoundPrefs, resolved: Resolved | undefined, page: number, moods: Mood[]): Mood | null {
  if (prefs.mode === "off") return null;
  if (prefs.mode === "manual") return moods.find((m) => m.id === prefs.moodId) ?? null;
  if (!resolved || resolved.muted) return null;
  let mood = resolved.mood ?? null;
  for (const cue of resolved.pages ?? []) if (cue.page <= page) mood = cue.mood ?? null;
  return mood;
}

/** tint is a mood colour at an opacity (00-ff), for backgrounds and borders. */
export const tint = (color: string, alpha: string) => (/^#[0-9a-fA-F]{6}$/.test(color) ? color + alpha : color);
