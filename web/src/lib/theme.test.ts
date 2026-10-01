import { describe, expect, it } from 'vitest';
import { accentVars, contrast } from './theme';

describe('accentVars', () => {
  it('keeps white text on the fill and accent text on the page readable', () => {
    for (const accent of ['#e8663d', '#f59e0b', '#10b981', '#ffff00', '#1e3a8a']) {
      for (const mode of ['dark', 'light'] as const) {
        const v = accentVars(accent, mode)!;
        expect(contrast(v['--color-primary'], '#ffffff')).toBeGreaterThanOrEqual(4.5);
        expect(contrast(v['--color-accent-2'], mode === 'dark' ? '#0f1115' : '#f6f7f9')).toBeGreaterThanOrEqual(4.5);
      }
    }
  });
  it('ignores anything but #rrggbb', () => {
    expect(accentVars('blue', 'dark')).toBeNull();
  });
});
