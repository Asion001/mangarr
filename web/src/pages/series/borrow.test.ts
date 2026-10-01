import { describe, expect, it } from 'vitest';
import type { Chapter, S } from '../../api/client';
import { borrowChapters } from './borrow';
const ch = (id: number, n: number, file = true) => ({ id, numberSort: n, number: String(n), title: '', file: file ? {} : undefined }) as unknown as Chapter;
const en = { id: 2, language: 'en', title: 'EN', coverUrl: '' } as S['EditionSummary'];
const de = { id: 3, language: 'de', title: 'DE', coverUrl: '' } as S['EditionSummary'];
describe('borrowChapters', () => {
  it('takes downloaded chapters this edition has no file for, first edition winning, newest first', () => {
    const own = [ch(1, 1), ch(2, 2, false)];
    const got = borrowChapters(own, [{ edition: en, chapters: [ch(10, 1), ch(11, 2), ch(12, 3), ch(13, 4, false)] }, { edition: de, chapters: [ch(20, 3), ch(21, 5)] }]);
    expect(got.map((b) => [b.chapter.id, b.edition.language])).toEqual([[21, 'de'], [12, 'en'], [11, 'en']]);
  });
});
