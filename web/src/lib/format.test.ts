import { afterEach, describe, expect, it } from 'vitest';
import { setLocale } from './i18n/core';
import { calendarMonth, languageMatches, languageName, readingTime } from './format';
afterEach(()=>setLocale('en'));
describe('reading statistics formats',()=>{
  it('formats reading time as hours and minutes',()=>{
    expect(readingTime(0)).toBe('0 min');
    expect(readingTime(20)).toBe('< 1 min');
    expect(readingTime(45*60)).toBe('45 min');
    expect(readingTime(2*3600)).toBe('2 hr');
    expect(readingTime(3*3600+5*60+10)).toBe('3 hr 5 min');
  });
  it('uses the interface locale',()=>{
    setLocale('ru');
    expect(readingTime(3*3600+5*60)).toBe('3 ч 5 мин');
    expect(languageName('en')).toBe('Английский');
    setLocale('uk');
    expect(readingTime(3*3600+5*60)).toBe('3 год 5 хв');
    expect(calendarMonth('2026-09')).toBe(new Intl.DateTimeFormat('uk',{month:'long',year:'numeric',timeZone:'UTC'}).format(Date.UTC(2026,8,1)));
    expect(languageName('und')).toBe('Невідома мова');
    expect(languageName('multi')).toBe('Кілька мов');
  });
  it('keeps unknown values readable',()=>{
    expect(calendarMonth('2026-09')).toBe('September 2026');
    expect(calendarMonth('bad')).toBe('bad');
    expect(languageName('ja')).toBe('Japanese');
    expect(languageName('not a tag')).toBe('not a tag');
  });
  it('matches language codes and localized names',()=>{
    setLocale('ru');
    expect(languageMatches('en','en')).toBe(true);
    expect(languageMatches('en','англ')).toBe(true);
    expect(languageMatches('en','япон')).toBe(false);
  });
});
