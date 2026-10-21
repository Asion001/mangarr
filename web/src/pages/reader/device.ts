import type { ReaderSettings } from "./settings";

type Saved = Partial<ReaderSettings>;

/** Device is what one browser keeps for an account: its defaults and each series' own settings (null: none of its own). */
export type Device = { defaults?: Saved; series: Record<string, Saved | null> };

// what was written, for a browser that keeps no site data (setItem throws)
const memory = new Map<string, Device>();

/** readDevice loads this browser's reader settings for an account. */
export function readDevice(key: string): Device {
  try {
    const item = localStorage.getItem(key);
    if (item === null && memory.has(key)) return memory.get(key)!;
    const v = JSON.parse(item || "{}");
    return {
      defaults: v.defaults && typeof v.defaults === "object" ? v.defaults : undefined,
      series: v.series && typeof v.series === "object" ? v.series : {},
    };
  } catch {
    return memory.get(key) ?? { series: {} };
  }
}

export function writeDevice(key: string, d: Device) {
  memory.set(key, d);
  try {
    localStorage.setItem(key, JSON.stringify(d));
  } catch {
    /* no storage: the settings last until the reader closes */
  }
}

/** seedDevice fills in what this browser hasn't kept yet from the settings saved on the account, once. */
export function seedDevice(d: Device, seriesId: number, saved: { defaults?: Saved | null; series?: Saved | null }): Device {
  return {
    defaults: d.defaults ?? saved.defaults ?? {},
    series: seriesId in d.series ? d.series : { ...d.series, [seriesId]: saved.series ?? null },
  };
}
