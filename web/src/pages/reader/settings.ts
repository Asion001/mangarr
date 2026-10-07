import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "../../api/client";

/** ReaderSettings are the web reader's options (saved per account and per series). */
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
  /** Pages loaded ahead. */
  preload: number;
  keepAwake: boolean;
  /** Go on to the next chapter at the end. */
  autoNext: boolean;
  showPageNumber: boolean;
  /** The back button and the series and chapter title in the top bar. */
  showTitle: boolean;
  /** A small full screen button in the corner while the bars are hidden. */
  quickFullscreen: boolean;
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
  keepAwake: true,
  autoNext: true,
  showPageNumber: true,
  showTitle: true,
  quickFullscreen: true,
};

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

/** useReaderSettings loads the effective settings for a series and saves changes for it. */
export function useReaderSettings(seriesId: number, seriesDirection: string) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["reader-settings", seriesId],
    queryFn: () => unwrap(api.GET("/api/v1/read/settings", { params: { query: { seriesId } } })),
    enabled: seriesId > 0,
    staleTime: Infinity,
  });
  const defaults = (data?.defaults ?? {}) as Saved;
  const own = (data?.series ?? null) as Saved | null;
  // defaults < the series' own direction < what you chose for this series
  const settings: ReaderSettings = { ...DEFAULTS, ...defaults, ...fromSeries(seriesDirection), ...(own ?? {}) };
  const put = (body: { seriesId?: number; data?: Saved }) => unwrap(api.PUT("/api/v1/read/settings", { body })).catch(() => undefined);
  /** set changes settings for this series. */
  const set = (patch: Saved) => {
    const next = { ...(own ?? {}), ...patch };
    qc.setQueryData(["reader-settings", seriesId], { defaults, series: next });
    void put({ seriesId, data: next });
  };
  /** saveAsDefault makes the current settings everyone's default (for series without their own). */
  const saveAsDefault = () => {
    const next: Saved = { ...settings };
    delete next.mode;
    delete next.direction;
    qc.setQueryData(["reader-settings", seriesId], { defaults: next, series: own });
    void put({ seriesId: 0, data: next });
  };
  /** reset drops this series' own settings. */
  const reset = () => {
    qc.setQueryData(["reader-settings", seriesId], { defaults, series: null });
    void put({ seriesId });
  };
  return { settings, loading: isLoading, hasOwn: !!own, set, saveAsDefault, reset };
}
