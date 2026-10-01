import { getLocale, t } from "./i18n/core";
export function bytes(n?: number | null): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${new Intl.NumberFormat(getLocale(), {maximumFractionDigits:v >= 10 || i === 0 ? 0 : 1}).format(v)} ${units[i]}`;
}

export function date(s?: string | null): string {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d.getTime()) || d.getFullYear() < 1971) return "—";
  return d.toLocaleDateString(getLocale());
}

export function dateTime(s?: string | null): string {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d.getTime()) || d.getFullYear() < 1971) return "—";
  return d.toLocaleString(getLocale());
}

export function relative(s?: string | null): string {
  if (!s) return t("never");
  const d = new Date(s).getTime();
  if (!Number.isFinite(d)) return t("never");
  const diff = (d - Date.now()) / 1000;
  const abs = Math.abs(diff);
  const format = new Intl.RelativeTimeFormat(getLocale(), {numeric:"auto"});
  if (abs < 60) return format.format(0, "second");
  if (abs < 3600) return format.format(Math.round(diff / 60), "minute");
  if (abs < 86400) return format.format(Math.round(diff / 3600), "hour");
  return format.format(Math.round(diff / 86400), "day");
}

export function duration(ms?: number | null): string {
  if (!ms) return "0s";
  if (ms < 1000) return `${ms}ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}

export function titleCase(s: string): string {
  return s.replace(/[-_.]/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

/** readingTime formats active reading seconds as hours and minutes ("3 hr 5 min"). */
export function readingTime(seconds?: number | null): string {
  const s = Math.max(0, Math.round(seconds ?? 0));
  const unit = (value: number, unit: "hour" | "minute") => new Intl.NumberFormat(getLocale(), { style: "unit", unit, unitDisplay: "short" }).format(value);
  if (s > 0 && s < 60) return `< ${unit(1, "minute")}`;
  const minutes = Math.round(s / 60);
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  if (!h) return unit(m, "minute");
  return m ? `${unit(h, "hour")} ${unit(m, "minute")}` : unit(h, "hour");
}

/** calendarMonth names a UTC "YYYY-MM" month ("September 2026"). */
export function calendarMonth(month: string): string {
  const [y, m] = month.split("-").map(Number);
  if (!y || !m) return month;
  return new Intl.DateTimeFormat(getLocale(), { month: "long", year: "numeric", timeZone: "UTC" }).format(Date.UTC(y, m - 1, 1));
}

/** languageName names a language code in the interface language ("en" → "English"). */
export function languageName(code: string): string {
  const normalized = code.trim().toLowerCase();
  if (!normalized || normalized === "und") return t("Unknown language");
  if (normalized === "all" || normalized === "multi") return t("Several languages");
  if (normalized === "other") return t("Other languages");
  try {
    const name = new Intl.DisplayNames(getLocale(), { type: "language" }).of(normalized);
    if (name && name !== normalized) return name.charAt(0).toLocaleUpperCase(getLocale()) + name.slice(1);
  } catch {
    // not a valid BCP 47 tag: show it as the source wrote it
  }
  return code;
}

/** sortLanguages orders codes by their names in the interface language. */
export function sortLanguages(codes: Iterable<string>): string[] {
  return [...new Set(codes)].sort((a, b) => languageName(a).localeCompare(languageName(b), getLocale()));
}

/** languageHints returns the codes whose names clash with another code's
 * in the list ("mo" and "ro" are both Romanian), which show their code too. */
export function languageHints(codes: string[]): Set<string> {
  const byName = new Map<string, string[]>();
  for (const c of codes) {
    const n = languageName(c).toLocaleLowerCase(getLocale());
    byName.set(n, [...(byName.get(n) ?? []), c]);
  }
  return new Set([...byName.values()].filter((cs) => cs.length > 1).flat());
}

/** languageMatches lets language filters find both the saved code and its localized name. */
export function languageMatches(code: string, query: string): boolean {
  const q = query.trim().toLocaleLowerCase(getLocale());
  return !q || code.toLocaleLowerCase(getLocale()).includes(q) || languageName(code).toLocaleLowerCase(getLocale()).includes(q);
}

/** browserLanguages maps the browser's languages onto catalog languages:
 * the exact tag when a catalog has it ("pt-BR"), else its base ("pt"). */
export function browserLanguages(available: string[], preferred: readonly string[] = typeof navigator === "undefined" ? [] : navigator.languages ?? []): string[] {
  const have = new Map(available.map((code) => [code.toLowerCase(), code]));
  const out: string[] = [];
  for (const tag of preferred) {
    const code = have.get(tag.toLowerCase()) ?? have.get(tag.toLowerCase().split("-")[0]);
    if (code && !out.includes(code)) out.push(code);
  }
  return out;
}
