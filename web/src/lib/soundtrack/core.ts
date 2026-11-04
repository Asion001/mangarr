// Soundtrack choices kept on this device: whether sound plays (off until a
// reader turns it on), how it follows the story, whether it's Spotify music
// or ambience only, the volumes and where music plays. Nothing here talks to
// the server.
import { useSyncExternalStore } from "react";
import type { S } from "../../api/client";

export type Mood = S["AudioMood"];
export type Resolved = S["SoundtrackResolved"];
export type Sound = S["SoundtrackSound"];
export type MoodTrack = S["MoodTrack"];

/** auto follows the title's soundtrack; manual keeps the mood picked by hand. */
export type SoundMode = "auto" | "manual" | "off";

/** spotify plays each mood's Spotify music (with its ambience under it); ambience plays only its loops and effects. */
export type SoundSource = "spotify" | "ambience";

export type SoundPrefs = {
  mode: SoundMode;
  /** the mode the speaker button turns back on */
  lastMode: Exclude<SoundMode, "off">;
  /** the mood picked by hand (manual mode) */
  moodId: number | null;
  /** what plays (ambience whenever Spotify isn't connected) */
  source: SoundSource;
  /** music volume, 0-100 */
  volume: number;
  /** ambience and effects volume, 0-100 */
  ambience: number;
  /** the Spotify device music plays on ("" = this browser, or the active one) */
  deviceId: string;
  /** which ambience loops play: calm only those marked always (softer), intense adds those marked intense (louder) */
  intensity: Intensity;
};

export type Intensity = "calm" | "normal" | "intense";
export const intensities: Intensity[] = ["calm", "normal", "intense"];

export const defaultPrefs: SoundPrefs = { mode: "off", lastMode: "auto", moodId: null, source: "spotify", volume: 70, ambience: 60, deviceId: "", intensity: "normal" };

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
    source: v.source === "ambience" ? "ambience" : "spotify",
    volume: clampVolume(typeof v.volume === "number" ? v.volume : defaultPrefs.volume),
    ambience: clampVolume(typeof v.ambience === "number" ? v.ambience : defaultPrefs.ambience),
    deviceId: typeof v.deviceId === "string" ? v.deviceId : "",
    intensity: intensities.includes(v.intensity as Intensity) ? (v.intensity as Intensity) : "normal",
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

/** soundOfMood is what a mood picked by hand plays. */
export function soundOfMood(m: Mood): Sound {
  return { source: "personal", mood: m, music: m.spotifyUri ? { uri: m.spotifyUri, name: m.spotifyName, imageUrl: m.spotifyImageUrl, shuffle: m.shuffle } : undefined };
}

/**
 * currentSound is what should play: in auto mode the title's soundtrack (a
 * page cue once the reader reaches it), in manual mode the mood picked.
 */
export function currentSound(prefs: SoundPrefs, resolved: Resolved | undefined, page: number, moods: Mood[]): Sound | null {
  if (prefs.mode === "off") return null;
  if (prefs.mode === "manual") {
    const m = moods.find((x) => x.id === prefs.moodId);
    return m ? soundOfMood(m) : null;
  }
  if (!resolved || resolved.muted) return null;
  let sound = resolved.start ?? null;
  for (const cue of resolved.pages ?? []) if (cue.page <= page) sound = cue.sound ?? null;
  return sound;
}

/** upcoming is the page cues starting within `ahead` pages after page (for prefetching). */
export function upcoming(resolved: Resolved | undefined, page: number, ahead = 3): Sound[] {
  return (resolved?.pages ?? []).filter((c) => c.page > page && c.page <= page + ahead && c.sound).map((c) => c.sound as Sound);
}

/** soundName names what plays: its mood. */
export const soundName = (s: Sound | null | undefined, moodName: (m: Mood) => string) => (s?.mood ? moodName(s.mood) : "");

/** sourceOf is what plays for a reader: Spotify music only when it's connected and picked. */
export const sourceOf = (prefs: SoundPrefs, connected: boolean): SoundSource => (connected && prefs.source === "spotify" ? "spotify" : "ambience");

/** soundColor is what plays' colour: its mood's. */
export const soundColor = (s: Sound | null | undefined) => s?.mood?.color ?? "#9aa2b1";

/** dbGain turns a pack's gain in dB into a gain factor. */
export const dbGain = (db: number) => Math.pow(10, (db || 0) / 20);

/** Layer is an ambience loop at a gain. */
export type Layer = { id: number; url: string; gain: number };

/** diffLayers says which loops to start, stop and fade to a new gain. */
export function diffLayers(from: Layer[], to: Layer[]) {
  const before = new Map(from.map((l) => [l.id, l]));
  const after = new Map(to.map((l) => [l.id, l]));
  return {
    start: to.filter((l) => !before.has(l.id)),
    stop: from.filter((l) => !after.has(l.id)),
    change: to.filter((l) => before.has(l.id) && before.get(l.id)!.gain !== l.gain),
  };
}

/** intensityGain is how much softer or louder ambience plays at an intensity. */
export const intensityGain: Record<Intensity, number> = { calm: 0.75, normal: 1, intense: 1.25 };

/**
 * playingLoops are the ambience loops that play at an intensity: calm only
 * those marked always (all of the normal ones when none is), normal those
 * and the unmarked ones, intense every loop.
 */
export function playingLoops(loops: MoodTrack[], intensity: Intensity = "normal"): MoodTrack[] {
  const normal = loops.filter((t) => t.plays !== "intense");
  if (intensity === "intense") return loops;
  if (intensity === "calm") {
    const always = loops.filter((t) => t.plays === "always");
    return always.length > 0 ? always : normal;
  }
  return normal;
}

/** layersOf are a sound's ambience loops as the engine plays them. */
export function layersOf(s: Sound | null | undefined, url: (trackId: number) => string, intensity: Intensity = "normal"): Layer[] {
  const k = intensityGain[intensity];
  return playingLoops(s?.mood?.ambience ?? [], intensity).map((t) => ({ id: t.trackId, url: url(t.trackId), gain: (t.volume ?? 1) * dbGain(t.gainDb) * k }));
}

/** tint is a mood colour at an opacity (00-ff), for backgrounds and borders. */
export const tint = (color: string, alpha: string) => (/^#[0-9a-fA-F]{6}$/.test(color) ? color + alpha : color);
