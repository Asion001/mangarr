import { describe, expect, it } from 'vitest';
import { currentSound, dbGain, defaultPrefs, diffLayers, isPhone, layersOf, parsePrefs, turnOn, upcoming, withMode, type Mood, type Resolved, type Sound } from './core';

const mood = (id: number, name: string): Mood => ({ id, name, slug: '', color: '#3fb98a', icon: 'leaf', genres: [], keywords: [], prompts: [], spotifyUri: '', spotifyName: '', spotifyImageUrl: '', shuffle: true, position: id, createdAt: '', updatedAt: '' });

describe('parsePrefs', () => {
  it('starts with music off', () => {
    expect(parsePrefs(null)).toEqual(defaultPrefs);
    expect(parsePrefs('not json')).toEqual(defaultPrefs);
  });
  it('keeps what makes sense', () => {
    expect(parsePrefs(JSON.stringify({ mode: 'manual', lastMode: 'manual', moodId: 3, volume: 140, deviceId: 'phone' }))).toEqual({ mode: 'manual', lastMode: 'manual', moodId: 3, volume: 100, ambience: 60, deviceId: 'phone' });
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

describe('currentSound', () => {
  const calm = mood(1, 'Calm');
  const storm = mood(2, 'Storm');
  const s = (m: Mood): Sound => ({ source: 'shared', mood: m });
  const resolved: Resolved = { source: 'shared', muted: false, start: s(calm), pages: [{ page: 8, sound: s(storm) }] };
  const on = withMode(defaultPrefs, 'auto');
  it('plays nothing while off or muted', () => {
    expect(currentSound(defaultPrefs, resolved, 1, [calm, storm])).toBeNull();
    expect(currentSound(on, { ...resolved, muted: true }, 1, [calm])).toBeNull();
  });
  it('follows the soundtrack, page cues included', () => {
    expect(currentSound(on, resolved, 7, [])?.mood?.name).toBe('Calm');
    expect(currentSound(on, resolved, 8, [])?.mood?.name).toBe('Storm');
  });
  it('keeps the mood picked by hand', () => {
    expect(currentSound({ ...withMode(on, 'manual'), moodId: 2 }, resolved, 1, [calm, storm])?.mood?.name).toBe('Storm');
  });
  it('looks a few pages ahead for prefetching', () => {
    expect(upcoming(resolved, 4)).toHaveLength(0);
    expect(upcoming(resolved, 5)).toHaveLength(1);
    expect(upcoming(resolved, 8)).toHaveLength(0);
  });
});

describe('ambience layers', () => {
  it('turns scene tracks into gains', () => {
    const scene = { id: 1, name: 'Rain', description: '', spotifyUri: '', spotifyName: '', spotifyImageUrl: '', shuffle: true, updatedAt: '', sfx: [],
      ambience: [{ trackId: 7, title: 'Rain', category: 'weather', icon: 'cloud-rain', sha256: 'a', ext: 'ogg', gainDb: -6, volume: 0.5, pack: 'P', license: '', attribution: '', sourceUrl: '' }] };
    const layers = layersOf({ source: 'shared', scene }, (id) => `/t/${id}`);
    expect(layers[0].url).toBe('/t/7');
    expect(layers[0].gain).toBeCloseTo(0.5 * dbGain(-6));
    expect(dbGain(0)).toBe(1);
  });
  it('crossfades only what changed', () => {
    const d = diffLayers([{ id: 1, url: 'a', gain: 1 }, { id: 2, url: 'b', gain: 1 }], [{ id: 2, url: 'b', gain: 0.5 }, { id: 3, url: 'c', gain: 1 }]);
    expect(d.start.map((l) => l.id)).toEqual([3]);
    expect(d.stop.map((l) => l.id)).toEqual([1]);
    expect(d.change.map((l) => l.id)).toEqual([2]);
  });
});
