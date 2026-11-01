import { describe, expect, it } from 'vitest';
import { currentMood, defaultPrefs, isPhone, parsePrefs, turnOn, withMode, type Mood, type Resolved } from './core';

const mood = (id: number, name: string): Mood => ({ id, name, slug: '', color: '#3fb98a', icon: 'leaf', genres: [], spotifyUri: '', spotifyName: '', spotifyImageUrl: '', shuffle: true, position: id, createdAt: '', updatedAt: '' });

describe('parsePrefs', () => {
  it('starts with music off', () => {
    expect(parsePrefs(null)).toEqual(defaultPrefs);
    expect(parsePrefs('not json')).toEqual(defaultPrefs);
  });
  it('keeps what makes sense', () => {
    expect(parsePrefs(JSON.stringify({ mode: 'manual', lastMode: 'manual', moodId: 3, volume: 140, deviceId: 'phone' }))).toEqual({ mode: 'manual', lastMode: 'manual', moodId: 3, volume: 100, deviceId: 'phone' });
    expect(parsePrefs(JSON.stringify({ mode: 'loud', moodId: -1, volume: 'x' }))).toEqual(defaultPrefs);
  });
});

describe('modes', () => {
  it('the speaker turns back on the last mode', () => {
    const manual = withMode(defaultPrefs, 'manual');
    const off = withMode(manual, 'off');
    expect(off.lastMode).toBe('manual');
    expect(turnOn(off).mode).toBe('manual');
    expect(turnOn(defaultPrefs).mode).toBe('auto');
  });
});

describe('isPhone', () => {
  it('tells phones and tablets from computers', () => {
    expect(isPhone('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Mobile/15E148')).toBe(true);
    expect(isPhone('Mozilla/5.0 (Linux; Android 15; Pixel 9) Chrome/140.0 Mobile Safari/537.36')).toBe(true);
    expect(isPhone('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15', 5)).toBe(true);
    expect(isPhone('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/140.0 Safari/537.36', 0)).toBe(false);
    expect(isPhone('Mozilla/5.0 (Windows NT 10.0; Win64; x64) Firefox/140.0')).toBe(false);
  });
});

describe('currentMood', () => {
  const calm = mood(1, 'Calm');
  const storm = mood(2, 'Storm');
  const resolved: Resolved = { source: 'shared', muted: false, mood: calm, pages: [{ page: 8, mood: storm }] };
  const on = withMode(defaultPrefs, 'auto');
  it('plays nothing while off or muted', () => {
    expect(currentMood(defaultPrefs, resolved, 1, [calm, storm])).toBeNull();
    expect(currentMood(on, { ...resolved, muted: true }, 1, [calm])).toBeNull();
  });
  it('follows the soundtrack, page cues included', () => {
    expect(currentMood(on, resolved, 7, [])?.name).toBe('Calm');
    expect(currentMood(on, resolved, 8, [])?.name).toBe('Storm');
  });
  it('keeps the mood picked by hand', () => {
    expect(currentMood({ ...withMode(on, 'manual'), moodId: 2 }, resolved, 1, [calm, storm])?.name).toBe('Storm');
  });
});
