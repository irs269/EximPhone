import { afterEach, describe, expect, it } from 'vitest';
import { dateLocale, setLocale, t } from './index';
import { en } from './en';
import { fr } from './fr';

afterEach(() => setLocale('fr'));

describe('t', () => {
  it('interpolates variables', () => expect(t('bye.title', { time: '17:02' })).toBe('Départ enregistré à 17:02'));
  it('falls back to French for untranslated locales', () => {
    setLocale('sw');
    expect(t('nav.messages')).toBe('Messages');
  });
  it('translates to English and switches back', () => {
    setLocale('en');
    expect(t('nav.calls')).toBe('Calls');
    expect(t('bye.title', { time: '17:02' })).toBe('Departure recorded at 17:02');
    expect(dateLocale()).toBe('en-GB');
    setLocale('fr');
    expect(t('nav.calls')).toBe('Appels');
  });
  it('has an English entry for every French key, with the same placeholders', () => {
    const vars = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort().join();
    for (const k of Object.keys(fr) as (keyof typeof fr)[]) {
      expect(en[k], k).toBeTruthy();
      expect(vars(en[k]), k).toBe(vars(fr[k]));
    }
  });
});
