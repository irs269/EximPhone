import type { ReactNode } from 'react';

type Style = 'fill' | 'stroke';
const defs: Record<string, [Style, ReactNode]> = {
  chat: ['stroke', <path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z" />],
  phone: ['stroke', <path d="M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1.9.4 1.8.7 2.7a2 2 0 0 1-.5 2.1L8 9.8a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.7.7a2 2 0 0 1 1.7 2z" />],
  dir: ['stroke', <><circle cx="9" cy="7" r="4" /><path d="M3 21v-2a4 4 0 0 1 4-4h4a4 4 0 0 1 4 4v2M16 3.1a4 4 0 0 1 0 7.8M21 21v-2a4 4 0 0 0-3-3.9" /></>],
  clock: ['stroke', <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></>],
  ann: ['stroke', <path d="M3 11l18-8v18L3 13zM7 12.5V19a2 2 0 0 0 4 0v-4.7" />],
  logout: ['stroke', <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" />],
  chart: ['stroke', <path d="M4 20V10M10 20V4M16 20v-8M22 20H2" />],
  shield: ['stroke', <path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z" />],
  call: ['fill', <path d="M6.6 10.8a15.1 15.1 0 0 0 6.6 6.6l2.2-2.2a1 1 0 0 1 1-.25 11.4 11.4 0 0 0 3.6.57 1 1 0 0 1 1 1V20a1 1 0 0 1-1 1A17 17 0 0 1 3 4a1 1 0 0 1 1-1h3.5a1 1 0 0 1 1 1c0 1.25.2 2.45.57 3.57a1 1 0 0 1-.25 1z" />],
  send: ['fill', <path d="M2 21l21-9L2 3v7l15 2-15 2z" />],
  desk: ['stroke', <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M7 4v16M11 8h6M11 12h2M15 12h2M11 16h2M15 16h2" /></>],
  pc: ['stroke', <><rect x="2" y="3" width="20" height="14" rx="2" /><path d="M8 21h8M12 17v4" /></>],
  mic: ['stroke', <><rect x="9" y="2" width="6" height="12" rx="3" /><path d="M5 10a7 7 0 0 0 14 0M12 17v5" /></>],
  pause: ['fill', <><rect x="6" y="4" width="4" height="16" /><rect x="14" y="4" width="4" height="16" /></>],
  transfer: ['stroke', <path d="M17 3l4 4-4 4M21 7H9M7 21l-4-4 4-4M3 17h12" />],
  grid: ['fill', <>{[5, 12, 19].flatMap((y) => [5, 12, 19].map((x) => <circle key={`${x}${y}`} cx={x} cy={y} r="2" />))}</>],
  ticket: ['stroke', <path d="M3 8a2 2 0 0 0 2-2h14a2 2 0 0 0 2 2v2a2 2 0 0 0 0 4v2a2 2 0 0 0-2 2H5a2 2 0 0 0-2-2v-2a2 2 0 0 0 0-4zM9 6v12" />],
  help: ['stroke', <><circle cx="12" cy="12" r="9" /><path d="M9.5 9.5a2.5 2.5 0 1 1 3.6 2.2c-.7.4-1.1 1-1.1 1.8M12 17h.01" /></>],
  lock: ['fill', <path d="M6 10V8a6 6 0 1 1 12 0v2h1a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V11a1 1 0 0 1 1-1zm2 0h8V8a4 4 0 1 0-8 0z" />],
};

export type IconName = keyof typeof defs;

export function Icon({ name, size, rotate }: { name: IconName; size?: number; rotate?: number }) {
  const [style, body] = defs[name];
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false"
      fill={style === 'fill' ? 'currentColor' : 'none'} stroke={style === 'stroke' ? 'currentColor' : undefined}
      strokeWidth={style === 'stroke' ? 2 : undefined} style={rotate ? { transform: `rotate(${rotate}deg)` } : undefined}>
      {body}
    </svg>
  );
}
