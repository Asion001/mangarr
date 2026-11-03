import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Session } from './session';

describe('Session', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('carries on when the next chapter takes over', () => {
    const release = vi.fn();
    const s = new Session(() => ({ played: '' }), release, 1000);
    const drop = s.hold();
    s.value.played = 'spotify:playlist:a|dev';
    drop();
    const next = s.hold(); // the next chapter's reader
    vi.advanceTimersByTime(5000);
    expect(release).not.toHaveBeenCalled();
    expect(s.value.played).toBe('spotify:playlist:a|dev');
    next();
    vi.advanceTimersByTime(999);
    expect(release).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(release).toHaveBeenCalledWith({ played: 'spotify:playlist:a|dev' });
    expect(s.value.played).toBe('');
  });

  it('lets go once per hold', () => {
    const release = vi.fn();
    const s = new Session(() => 0, release, 10);
    const a = s.hold();
    const b = s.hold();
    a();
    a();
    vi.advanceTimersByTime(20);
    expect(release).not.toHaveBeenCalled();
    b();
    vi.advanceTimersByTime(20);
    expect(release).toHaveBeenCalledTimes(1);
  });
});
