import type { S } from "../../api/client";
import { getLocale, t } from "../../lib/i18n/core";

export type TaskSchedule = S["TaskSchedule"];
export type IntervalUnit = "minute" | "hour" | "day";

export const WEEKDAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"] as const;
const unitMinutes: Record<IntervalUnit, number> = { minute: 1, hour: 60, day: 1440 };

/** weekdayName names a weekday ("mon") in the interface language. */
export function weekdayName(day: string, style: "short" | "long" = "short"): string {
  const i = WEEKDAYS.indexOf(day as (typeof WEEKDAYS)[number]);
  if (i < 0) return day;
  // 1 January 2024 was a Monday
  return new Intl.DateTimeFormat(getLocale(), { weekday: style, timeZone: "UTC" }).format(new Date(Date.UTC(2024, 0, 1 + i)));
}

/** splitInterval picks the largest unit that divides the interval evenly; a day stays "24 hours". */
export function splitInterval(minutes: number): { value: number; unit: IntervalUnit } {
  if (minutes > 1440 && minutes % 1440 === 0) return { value: minutes / 1440, unit: "day" };
  if (minutes >= 60 && minutes % 60 === 0) return { value: minutes / 60, unit: "hour" };
  return { value: minutes, unit: "minute" };
}

export const toMinutes = (value: number, unit: IntervalUnit) => Math.round(value * unitMinutes[unit]);

/** intervalText reads an interval as "30 minutes" or "24 hours". */
export function intervalText(minutes: number): string {
  const { value, unit } = splitInterval(minutes);
  return new Intl.NumberFormat(getLocale(), { style: "unit", unit, unitDisplay: "long" }).format(value);
}

const list = (items: string[]) => new Intl.ListFormat(getLocale(), { style: "long", type: "conjunction" }).format(items);

/** allDays is true when a weekday list means every day (none or all seven picked). */
export const allDays = (days?: string[] | null) => !days || days.length === 0 || WEEKDAYS.every((d) => days.includes(d));

/** scheduleText reads a schedule the way the Tasks page shows it: "Every 30 minutes", "Mon and Thu at 05:00". */
export function scheduleText(s?: TaskSchedule | null): string {
  if (!s) return "—";
  if (s.kind === "interval") {
    const minutes = s.intervalMinutes ?? 0;
    if (minutes === 1) return t("Every minute");
    if (minutes === 60) return t("Every hour");
    return t("Every {interval}", { interval: intervalText(minutes) });
  }
  const times = list([...(s.timesOfDay ?? [])].sort());
  if (allDays(s.weekdays)) return t("Daily at {times}", { times });
  const days = WEEKDAYS.filter((d) => s.weekdays?.includes(d)).map((d) => weekdayName(d));
  return t("{days} at {times}", { days: list(days), times });
}

/** validSchedule returns what is wrong with a schedule, or "" when it can be saved. */
export function scheduleProblem(s: TaskSchedule, minIntervalMinutes: number): string {
  if (s.kind === "interval") {
    const m = s.intervalMinutes ?? 0;
    if (!Number.isFinite(m) || m < minIntervalMinutes) return t("Run at most every {interval}", { interval: intervalText(minIntervalMinutes) });
    if (m > 525600) return t("Run at least once a year");
    return "";
  }
  const times = s.timesOfDay ?? [];
  if (times.length === 0) return t("Add at least one time");
  if (times.some((x) => !/^([01]\d|2[0-3]):[0-5]\d$/.test(x))) return t("Use times like 03:30");
  if (new Set(times).size !== times.length) return t("Each time can only be listed once");
  return "";
}

/** sameSchedule compares two schedules, ignoring the order of times and days. */
export function sameSchedule(a?: TaskSchedule | null, b?: TaskSchedule | null): boolean {
  if (!a || !b || a.kind !== b.kind) return false;
  if (a.kind === "interval") return (a.intervalMinutes ?? 0) === (b.intervalMinutes ?? 0);
  const key = (x?: string[] | null) => [...(x ?? [])].sort().join(",");
  const days = (x?: string[] | null) => (allDays(x) ? "" : key(x));
  return key(a.timesOfDay) === key(b.timesOfDay) && days(a.weekdays) === days(b.weekdays);
}

/** validZone returns a time zone Intl accepts, or undefined for the browser's own. */
function validZone(zone?: string): string | undefined {
  if (!zone) return undefined;
  try {
    new Intl.DateTimeFormat("en", { timeZone: zone });
    return zone;
  } catch {
    return undefined;
  }
}

/** runTime formats a run: "14:05" today, "Tomorrow 04:00", "Mon 05:00" within a week, else the date. */
export function runTime(iso?: string | null, timeZone?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "—";
  const locale = getLocale();
  const zone = validZone(timeZone);
  const day = (x: Date) => new Intl.DateTimeFormat("en-CA", { timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit" }).format(x);
  const clock = new Intl.DateTimeFormat(locale, { timeZone: zone, hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(d);
  const now = new Date();
  const days = Math.round((Date.parse(day(d)) - Date.parse(day(now))) / 86400000);
  if (days === 0) return t("Today {time}", { time: clock });
  if (days === 1) return t("Tomorrow {time}", { time: clock });
  if (days === -1) return t("Yesterday {time}", { time: clock });
  if (Math.abs(days) < 7) return `${new Intl.DateTimeFormat(locale, { timeZone: zone, weekday: "short" }).format(d)} ${clock}`;
  return `${new Intl.DateTimeFormat(locale, { timeZone: zone, day: "numeric", month: "short" }).format(d)} ${clock}`;
}

/** runDuration reads a duration in milliseconds as "38 s" or "4 min 12 s". */
export function runDuration(ms?: number | null): string {
  const s = Math.max(0, Math.round((ms ?? 0) / 1000));
  const unit = (value: number, unit: "hour" | "minute" | "second") => new Intl.NumberFormat(getLocale(), { style: "unit", unit, unitDisplay: "short" }).format(value);
  if (s < 60) return unit(s, "second");
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h > 0) return m ? `${unit(h, "hour")} ${unit(m, "minute")}` : unit(h, "hour");
  return s % 60 ? `${unit(m, "minute")} ${unit(s % 60, "second")}` : unit(m, "minute");
}

/** triggerText reads a command trigger: "Scheduled", "Manual · Roma", or whatever started it. */
export function triggerText(trigger: string): string {
  if (trigger === "scheduled") return t("Scheduled");
  if (trigger === "manual") return t("Manual");
  if (trigger.startsWith("manual · ")) return `${t("Manual")} · ${trigger.slice("manual · ".length)}`;
  return trigger;
}
