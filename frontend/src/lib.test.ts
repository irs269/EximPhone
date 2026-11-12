import { describe, expect, it } from 'vitest';
import { durMin, initials, mmss } from './lib';

describe('lib', () => {
  it('formats durations', () => { expect(durMin(550)).toBe('9 h 10 min'); expect(mmss(75)).toBe('01:15'); });
  it('builds initials', () => expect(initials('Amina Said')).toBe('AS'));
});
