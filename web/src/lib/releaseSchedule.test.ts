import { describe, expect, it } from 'vitest';
import { releaseSchedule } from './releaseSchedule';

const DAY = 86400_000;
const now = Date.UTC(2026, 9, 8, 12);
const ago = (days: number) => new Date(now - days * DAY).toISOString();
const chapter = (n: number, releasedDaysAgo?: number, seenDaysAgo = 0) => ({
  number: String(n), numberSort: n, releaseDate: releasedDaysAgo === undefined ? undefined : ago(releasedDaysAgo), firstSeenAt: ago(seenDaysAgo),
});
const weekly = (count: number, lastDaysAgo: number) => Array.from({ length: count }, (_, i) => chapter(i + 1, lastDaysAgo + (count - 1 - i) * 7));

describe('release schedule', () => {
  it('expects the next chapter one usual gap after the last', () => {
    const s = releaseSchedule(weekly(12, 2), 'ongoing', now)!;
    expect(s.state).toBe('regular');
    expect(s.last.number).toBe('12');
    expect(s.gapDays).toBe(7);
    expect(s.days).toHaveLength(11);
    expect(Math.round((s.next! - now) / DAY)).toBe(5);
  });
  it('flags a late chapter and a long silence', () => {
    expect(releaseSchedule(weekly(8, 10), 'ongoing', now)!.state).toBe('late');
    expect(releaseSchedule(weekly(8, 40), 'ongoing', now)!.state).toBe('break');
    expect(releaseSchedule(weekly(8, 1), 'hiatus', now)!.state).toBe('break');
  });
  it('gives a range when gaps vary a lot', () => {
    const gaps = [3, 20, 4, 25, 5, 22, 6];
    let at = 1;
    const chapters = [chapter(gaps.length + 1, at)];
    gaps.forEach((g, i) => { at += g; chapters.push(chapter(gaps.length - i, at)); });
    const s = releaseSchedule(chapters, 'ongoing', now)!;
    expect(s.state).toBe('irregular');
    expect(s.spread![0]).toBeLessThan(s.spread![1]);
  });
  it('counts a batch on one day as one release and falls back to first seen', () => {
    const batch = [chapter(1, undefined, 30), chapter(2, undefined, 30), chapter(3, undefined, 30), chapter(4, undefined, 2)];
    const s = releaseSchedule(batch, 'ongoing', now)!;
    expect(s.days).toHaveLength(2);
    expect(s.state).toBe('few');
    expect(s.last.number).toBe('4');
  });
  it('has no next chapter once the series ends', () => {
    const s = releaseSchedule(weekly(5, 100), 'completed', now)!;
    expect(s.state).toBe('finished');
    expect(s.next).toBeUndefined();
  });
  it('ignores missing and placeholder dates', () => {
    expect(releaseSchedule([], 'ongoing', now)).toBeNull();
    expect(releaseSchedule([{ number: '1', numberSort: 1, releaseDate: '0001-01-01T00:00:00Z', firstSeenAt: '0001-01-01T00:00:00Z' }], 'ongoing', now)).toBeNull();
  });
});
