import { Icon } from './Icon';
import type { Presence } from '../data/mock';
import type { DirUser } from '../api/client';
import { avatarColors } from '../theme/tokens';
import { initials } from '../lib';

export const presenceColor: Record<Presence, string> = {
  on: 'var(--ok)', busy: 'var(--alert)', meet: 'var(--warn)', away: 'var(--gold)', dnd: 'var(--alert-dark)',
};

export function GroupAvatar() {
  return <div className="av" style={{ background: 'var(--navy-2)' }}><Icon name="grid" size={18} /></div>;
}

export const statusColor = (s?: string) => (s && s in presenceColor ? presenceColor[s as Presence] : 'var(--offline)');

export function UserAvatar({ u, status, big }: { u: Pick<DirUser, 'id' | 'display_name'>; status?: string; big?: boolean }) {
  return (
    <div className="av" style={{ background: avatarColors[u.id % avatarColors.length],
      ...(big ? { width: 92, height: 92, fontSize: 30, margin: '-46px auto 0', border: '4px solid var(--surface)' } : null) }}>
      {initials(u.display_name)}
      <span className="dot" style={{ background: statusColor(status) }} />
    </div>
  );
}
