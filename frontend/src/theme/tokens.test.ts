import { describe, expect, it } from 'vitest';
import { applyTokens, tokens } from './tokens';

describe('tokens', () => {
  it('uses the official Exim Bank palette', () => {
    expect(tokens.navy).toBe('#002E6C');
    expect(tokens.navy2).toBe('#22317F');
    expect(tokens.gold).toBe('#A99564');
  });
  it('maps camelCase keys to CSS variables', () => {
    const el = { style: { setProperty: (k: string, v: string) => ((el as any)[k] = v) } } as unknown as HTMLElement;
    applyTokens(el);
    expect((el as any)['--gold-light']).toBe(tokens.goldLight);
    expect((el as any)['--ok-text']).toBe(tokens.okText);
  });
});
