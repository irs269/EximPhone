import { fr, type Key } from './fr';

export type Locale = 'fr' | 'en' | 'sw';
// English and Swahili catalogues are filled in later; missing keys fall back to French.
const catalogs: Record<Locale, Partial<Record<Key, string>>> = { fr, en: {}, sw: {} };
let locale: Locale = 'fr';

export const setLocale = (l: Locale) => { locale = l; };

export function t(key: Key, vars?: Record<string, string | number>): string {
  let s: string = catalogs[locale][key] ?? fr[key];
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replace(`{${k}}`, String(v));
  return s;
}
