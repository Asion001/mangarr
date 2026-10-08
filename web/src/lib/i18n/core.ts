// English is the keys themselves, so the translations load only when a
// browser needs them, and stay out of the first page load.
import type messages from './messages.json';
export type Locale = 'en' | 'ru' | 'uk';
export type LocalePreference = Locale | 'auto';
export type MessageKey = keyof typeof messages;
type Catalog = Record<string, {ru:string; uk:string}>;
let catalog: Catalog | null = null;
let loading: Promise<Catalog> | null = null;
export function loadCatalog(): Promise<Catalog> {
  loading ??= import('./messages.json').then(m => (catalog = m.default as Catalog)).catch(err => { loading = null; throw err; });
  return loading;
}
const storedLocale = 'mangarr:locale';
// start fetching the last language this browser showed before the app asks for it
try { const last = localStorage.getItem(storedLocale); if (last === 'ru' || last === 'uk') void loadCatalog().catch(() => {}); } catch { /* no storage */ }
let current: Locale = 'en';
let wanted: Locale = 'en';
const listeners = new Set<() => void>();
export const getLocale = () => current;
export const subscribeLocale = (fn: () => void) => { listeners.add(fn); return () => { listeners.delete(fn); }; };
export function resolveLocale(value: string, languages: readonly string[] = []): Locale {
  const choices = value === 'auto' ? languages : [value];
  for (const candidate of choices) {
    const lang = candidate.toLowerCase().split(/[-_]/)[0];
    if (lang === 'ua') return 'uk';
    if (lang === 'en' || lang === 'ru' || lang === 'uk') return lang;
  }
  return 'en';
}
/** Switches the interface language, once its translations have loaded. */
export function setLocale(value: Locale) {
  wanted = value;
  try { localStorage.setItem(storedLocale, value); } catch { /* no storage */ }
  if (value !== 'en' && !catalog) {
    void loadCatalog().then(() => { if (wanted === value) apply(value); }, () => {});
    return;
  }
  apply(value);
}
function apply(value: Locale) {
  if (typeof document !== 'undefined') document.documentElement.lang = value;
  if (value === current) return;
  current = value;
  listeners.forEach(fn => fn());
}
/** Translate only application-owned messages; never feed source titles through this. */
export function t(key: MessageKey, values: Record<string, string | number> = {}): string {
  return translate(key, current, values);
}
export function translate(key: string, locale: Locale, values: Record<string, string | number> = {}): string {
  const row = catalog?.[key];
  const text = locale === 'en' ? key : row?.[locale] ?? key;
  return text.replace(/\{(\w+)\}/g, (match, name: string) => values[name] === undefined ? match : String(values[name]));
}
/** Built-in dynamic form/status labels may include third-party fallback text. */
export const label = (text: string) => translate(text, current);
export function plural(count: number, forms: Partial<Record<Intl.LDMLPluralRule, string>> & {other:string}, locale = current) {
  return (forms[new Intl.PluralRules(locale).select(count)] ?? forms.other).replace(/\{count\}/g, new Intl.NumberFormat(locale).format(count));
}
