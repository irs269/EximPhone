// Exim Bank design tokens: the single source of truth for the brand.
// Applied to CSS custom properties at startup; components only use var(--...).
export const tokens = {
  navy: '#002E6C',
  navy2: '#22317F',
  gold: '#A99564',
  goldLight: '#EFE9DB',
  bg: '#F4F6FA',
  line: '#E3E7EF',
  text: '#1D2433',
  muted: '#6B7387',
  ok: '#21CE99',
  okText: '#0F8A63',
  okBg: '#DCF7EE',
  alert: '#F21053',
  alertBg: '#FDE3EB',
  alertDark: '#7A1030',
  warn: '#E8A33D',
  warnText: '#C2410C',
  warnBg: '#FFF4E5',
  warnBgText: '#9A4A00',
  offline: '#B8BFCC',
  surface: '#FFFFFF',
  onDark: '#C5CDE0',
  scrim: 'rgba(0,20,50,.55)',
  slate: '#5B6B8C',
  hover: '#F7F8FB',
  pillOffBg: '#EEF0F4',
  switchOff: '#CDD2DC',
  tagText: '#7A6A40',
  bodyText: '#3B4357',
  logoutText: '#FFB3C8',
  goldSoft: 'rgba(169,149,100,.25)',
  goldFaint: 'rgba(169,149,100,.16)',
  goldPulse: 'rgba(169,149,100,.7)',
  goldClear: 'rgba(169,149,100,0)',
  whiteSoft: 'rgba(255,255,255,.12)',
  whiteHover: 'rgba(255,255,255,.08)',
  shadowSm: 'rgba(0,0,0,.2)',
  shadowLg: 'rgba(0,0,0,.35)',
  shadowMenu: 'rgba(0,20,60,.25)',
  fontFamily: "Bliss, 'Segoe UI', Arial, sans-serif",
} as const;

export type Tokens = typeof tokens;

/** Avatar palette (cycled by index). */
export const avatarColors = [tokens.navy, tokens.gold, tokens.navy2, '#5B6B8C', '#8A7A4F', '#0060B4', '#3D4A7A'];

const cssName = (k: string) => '--' + k.replace(/[A-Z0-9]/g, (m) => '-' + m.toLowerCase());

export function applyTokens(root: HTMLElement = document.documentElement): void {
  for (const [k, v] of Object.entries(tokens)) root.style.setProperty(cssName(k), v);
}
