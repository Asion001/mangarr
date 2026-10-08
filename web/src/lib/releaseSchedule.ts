const DAY = 86400_000;

/** How many recent releases the estimate looks at. */
const WINDOW = 10;

export type ReleaseState = "few" | "regular" | "irregular" | "late" | "break" | "finished";

export type ReleaseSchedule = {
  state: ReleaseState;
  /** The newest chapter and when it came out. */
  last: { number: string; at: number };
  /** Release days in the window, oldest first (ms, start of day). */
  days: number[];
  /** Typical gap between releases, in days. */
  gapDays?: number;
  /** The usual spread of gaps, in days, when releases are irregular. */
  spread?: [number, number];
  /** When the next chapter is expected (ms). */
  next?: number;
};

type ChapterDates = { number: string; numberSort: number; releaseDate?: string; firstSeenAt: string };

/** releaseSchedule guesses when the next chapter comes out from the gaps
 * between recent releases. Chapters that came out on the same day count as
 * one release, so a batch upload or the first import doesn't read as a
 * zero-day cadence. Chapters without a release date use when they were
 * first seen. Nothing when no chapter has a date at all. */
export function releaseSchedule(chapters: ChapterDates[] | undefined, status: string, now = Date.now()): ReleaseSchedule | null {
  if (!chapters?.length) return null;
  let last: ReleaseSchedule["last"] | undefined;
  let lastSort = -Infinity;
  const daySet = new Set<number>();
  for (const c of chapters) {
    const at = dateOf(c.releaseDate) ?? dateOf(c.firstSeenAt);
    if (at === undefined || at > now + DAY) continue;
    daySet.add(Math.floor(at / DAY) * DAY);
    if (!last || at > last.at || (at === last.at && c.numberSort > lastSort)) {
      last = { number: c.number, at };
      lastSort = c.numberSort;
    }
  }
  if (!last) return null;
  const days = [...daySet].sort((a, b) => a - b).slice(-(WINDOW + 1));
  const finished = status === "completed" || status === "cancelled";
  if (finished) return { state: "finished", last, days };
  if (days.length < 3) return { state: "few", last, days };

  const gaps = days.slice(1).map((d, i) => (d - days[i]) / DAY).sort((a, b) => a - b);
  const gapDays = quantile(gaps, 0.5);
  const q1 = quantile(gaps, 0.25);
  const q3 = quantile(gaps, 0.75);
  const next = last.at + gapDays * DAY;
  const since = (now - last.at) / DAY;
  const base = { last, days, gapDays, next };
  if (status === "hiatus" || (since > gapDays * 3 && since > 14)) return { ...base, state: "break" };
  if (now > next + DAY) return { ...base, state: "late" };
  if (q3 > q1 * 2 && q3 - q1 >= 3) return { ...base, state: "irregular", spread: [Math.round(q1), Math.round(q3)] };
  return { ...base, state: "regular" };
}

function dateOf(s?: string): number | undefined {
  if (!s) return undefined;
  const at = new Date(s).getTime();
  return Number.isFinite(at) && at >= Date.UTC(1971, 0, 1) ? at : undefined;
}

function quantile(sorted: number[], q: number): number {
  const pos = (sorted.length - 1) * q;
  const lo = Math.floor(pos);
  const hi = Math.ceil(pos);
  return sorted[lo] + (sorted[hi] - sorted[lo]) * (pos - lo);
}
