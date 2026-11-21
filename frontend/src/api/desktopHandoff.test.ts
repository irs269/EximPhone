import { describe, expect, it } from 'vitest';
import { signinLink } from './desktopHandoff';

describe('signinLink', () => {
  it('carries only the one-time code, in the eximconnect scheme', () => {
    const code = 'A'.repeat(43);
    expect(signinLink(code)).toBe(`eximconnect://signin?code=${code}`);
  });
  it('escapes anything that is not a plain code', () => {
    expect(signinLink('a b&c=d')).toBe('eximconnect://signin?code=a%20b%26c%3Dd');
  });
});
