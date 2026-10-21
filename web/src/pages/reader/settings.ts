import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { api, unwrap } from "../../api/client";
import { useAccount } from "../../lib/account";
import { readDevice, seedDevice, writeDevice, type Device } from "./device";

/** ReaderSettings are the web reader's options (kept on each device, per account and per series). */
export type ReaderSettings = {
  /** paged: one screen at a time; webtoon: one long strip. */
  mode: "paged" | "webtoon";
  /** Page order in paged mode. */
  direction: "ltr" | "rtl" | "vertical";
  scale: "screen" | "width" | "height" | "original";
  /** Two pages side by side: never, always, or on wide screens. */
  spread: "single" | "double" | "auto";
  /** With spreads, show the first page alone (covers). */
  coverAlone: boolean;
  /** Crop white and black borders. */
  crop: boolean;
  /** Show wide (double) pages as two halves, in reading order. */
  splitWide: boolean;
  /** Webtoon: side padding in percent of the width. */
  padding: number;
  /** Webtoon: a gap between pages. */
  gap: boolean;
  tapZones: "auto" | "lshape" | "kindle" | "edge" | "leftright" | "off";
  invertTaps: boolean;
  background: "black" | "white" | "gray";
  /** Paged: pages loaded ahead. */
  preload: number;
  /** Webtoon: screens ahead of the one you're on that pages load from. */
  aheadScreens: number;
  /** Webtoon: how far a page-turn tap or Page Down scrolls, in screens. */
  tapScroll: number;
  /** Webtoon: auto scroll speed, 1 (slow) to 10 (fast). */
  autoScrollSpeed: number;
  /** Webtoon: start auto scrolling when a chapter opens. */
  autoScrollOnOpen: boolean;
  /** Chapters after the open one kept saved on this device while reading (the open one is always saved). */
  saveAhead: number;
  keepAwake: boolean;
  /** Go on to the next chapter at the end. */
  autoNext: boolean;
  showPageNumber: boolean;
  /** A small full screen button in the corner while the bars are hidden. */
  quickFullscreen: boolean;
  /** Show the tap zones for a moment when the reader opens or they change. */
  showTapZones: boolean;
};

export const DEFAULTS: ReaderSettings = {
  mode: "paged",
  direction: "rtl",
  scale: "screen",
  spread: "single",
  coverAlone: true,
  crop: false,
  splitWide: false,
  padding: 0,
  gap: false,
  tapZones: "auto",
  invertTaps: false,
  background: "black",
  preload: 4,
  aheadScreens: 5,
  tapScroll: 0.85,
  autoScrollSpeed: 4,
  autoScrollOnOpen: false,
  saveAhead: 3,
  keepAwake: true,
  autoNext: true,
  showPageNumber: true,
  quickFullscreen: true,
  showTapZones: true,
};

export const AUTO_SCROLL_MIN = 1;
export const AUTO_SCROLL_MAX = 10;

/** autoScrollPxPerSecond turns an auto scroll speed (1-10) into CSS pixels a second: 20 at 1, about 50 at 4, about 300 at 10. */
export function autoScrollPxPerSecond(speed: number): number {
  const level = Math.min(AUTO_SCROLL_MAX, Math.max(AUTO_SCROLL_MIN, Math.round(speed)));
  return 20 * 1.35 ** (level - 1);
}

/** fromSeries turns a series' reading direction into reader settings. */
export function fromSeries(dir: string): Partial<ReaderSettings> {
  switch (dir) {
    case "webtoon":
      return { mode: "webtoon" };
    case "vertical":
      return { mode: "paged", direction: "vertical" };
    case "ltr":
      return { mode: "paged", direction: "ltr" };
    case "rtl":
      return { mode: "paged", direction: "rtl" };
  }
  return {};
}

type Saved = Partial<ReaderSettings>;

/**
 * useReaderSettings loads the effective settings for a series and saves
 * changes for it. They're kept on this device, so a phone and a tablet can
 * read differently; a device that hasn't kept any yet starts from the ones
 * saved on the account.
 */
export function useReaderSettings(seriesId: number, seriesDirection: string) {
  const { account } = useAccount();
  const key = `mangarr:reader:${account?.kind ?? "guest"}:${account?.id ?? 0}`;
  const [version, setVersion] = useState(0);
  const device = useMemo(() => readDevice(key), [key, version]);
  const seeded = device.defaults !== undefined && seriesId in device.series;
  const { data, isLoading, failureCount } = useQuery({
    queryKey: ["reader-settings", seriesId],
    queryFn: () => unwrap(api.GET("/api/v1/read/settings", { params: { query: { seriesId } } })),
    enabled: !!account && seriesId > 0 && !seeded,
    staleTime: Infinity,
  });
  useEffect(() => {
    if (!data || !account || seeded) return;
    writeDevice(key, seedDevice(device, seriesId, data as { defaults?: Saved; series?: Saved | null }));
    setVersion((v) => v + 1);
  }, [data, account, seeded, key, device, seriesId]);
  const defaults = device.defaults ?? ((data?.defaults ?? {}) as Saved);
  const own = seriesId in device.series ? device.series[seriesId] : ((data?.series ?? null) as Saved | null);
  // defaults < the series' own direction < what you chose for this series
  const settings: ReaderSettings = { ...DEFAULTS, ...defaults, ...fromSeries(seriesDirection), ...(own ?? {}) };
  const save = (next: Device) => {
    writeDevice(key, next);
    setVersion((v) => v + 1);
  };
  /** set changes settings for this series. */
  // device.defaults, not defaults: until the account's are seeded, saving
  // the built-in ones would stop them ever being fetched here
  const set = (patch: Saved) => save({ defaults: device.defaults, series: { ...device.series, [seriesId]: { ...(own ?? {}), ...patch } } });
  /** saveAsDefault makes the current settings the default here (for series without their own). */
  const saveAsDefault = () => {
    const next: Saved = { ...settings };
    delete next.mode;
    delete next.direction;
    save({ defaults: next, series: { ...device.series, [seriesId]: own } });
  };
  /** reset drops this series' own settings. */
  const reset = () => save({ defaults: device.defaults, series: { ...device.series, [seriesId]: null } });
  // waiting stops at the first failed try: with no server (offline, or a
  // restart) the reader opens with what this device has instead of a spinner
  return { settings, loading: seriesId > 0 && !seeded && (!account || (isLoading && failureCount === 0)), hasOwn: !!own, set, saveAsDefault, reset };
}
