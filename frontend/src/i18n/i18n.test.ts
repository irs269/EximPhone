import { describe, expect, it } from 'vitest';
import { setLocale, t } from './index';

describe('t', () => {
  it('interpolates variables', () => expect(t('bye.title', { time: '17:02' })).toBe('Départ enregistré à 17:02'));
  it('falls back to French for untranslated locales', () => {
    setLocale('sw');
    expect(t('nav.messages')).toBe('Messages');
    setLocale('fr');
  });
});
