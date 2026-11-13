import { create } from 'zustand';
import { fr, type Key } from './fr';
import { en } from './en';

export type Locale = 'fr' | 'en' | 'sw';
// Languages offered in the interface. Swahili falls back to French until its catalogue is written.
export const selectable: Exclude<Locale, 'sw'>[] = ['fr', 'en'];
const catalogs: Record<Locale, Partial<Record<Key, string>>> = { fr, en, sw: {} };
const STORAGE_KEY = 'eximphone.locale';

function initial(): Locale {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === 'fr' || saved === 'en') return saved;
  } catch { /* storage unavailable: keep the default */ }
  return 'fr';
}

let locale: Locale = initial();
if (typeof document !== 'undefined') document.documentElement.lang = locale;

/** Subscribe with useLocale(s => s.locale) to re-render when the language changes. */
export const useLocale = create<{ locale: Locale }>(() => ({ locale }));

export function setLocale(l: Locale) {
  locale = l;
  if (typeof document !== 'undefined') document.documentElement.lang = l;
  try { localStorage.setItem(STORAGE_KEY, l); } catch { /* ignore */ }
  useLocale.setState({ locale: l });
}

export const getLocale = (): Locale => locale;
/** BCP 47 tag for Intl / toLocale*String. */
export const dateLocale = (): string => (locale === 'en' ? 'en-GB' : 'fr-FR');

export function t(key: Key, vars?: Record<string, string | number>): string {
  let s: string = catalogs[locale][key] ?? fr[key];
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replace(`{${k}}`, String(v));
  return s;
}
