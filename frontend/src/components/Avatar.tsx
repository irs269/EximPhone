import type { CSSProperties } from 'react';
import { Icon } from './Icon';
import type { Line, Person, Presence } from '../data/mock';

export const presenceColor: Record<Presence, string> = {
  on: 'var(--ok)', busy: 'var(--alert)', meet: 'var(--warn)', away: 'var(--gold)', dnd: 'var(--alert-dark)',
};
const lineColor: Record<Line, string> = { on: 'var(--ok)', busy: 'var(--alert)', off: 'var(--offline)' };
export const lineLabel: Record<Line, string> = { on: 'Disponible', busy: 'En communication', off: 'Hors ligne' };

export function Avatar({ p, big, dot = true, style }: { p: Pick<Person, 'ini' | 'color'> & { line?: Line }; big?: boolean; dot?: boolean; style?: CSSProperties }) {
  return (
    <div className="av" style={{ background: p.color, ...(big ? { width: 92, height: 92, fontSize: 30, margin: '-46px auto 0', border: '4px solid var(--surface)' } : null), ...style }}>
      {p.ini}
      {dot && p.line && <span className="dot" style={{ background: lineColor[p.line] }} />}
    </div>
  );
}

export function GroupAvatar() {
  return <div className="av" style={{ background: 'var(--navy-2)' }}><Icon name="grid" size={18} /></div>;
}
