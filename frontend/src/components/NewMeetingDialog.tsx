import { useState } from 'react';
import { t } from '../i18n';
import { useApp } from '../store/app';
import { useChat } from '../store/chat';
import { useMeetings } from '../store/meetings';
import { UserAvatar } from './Avatar';

export function NewMeetingDialog() {
  const { composing, setComposing, start } = useMeetings();
  const { users, presence, me } = useChat();
  const showToast = useApp((s) => s.showToast);
  const [q, setQ] = useState('');
  const [title, setTitle] = useState('');
  const [picked, setPicked] = useState<number[]>([]);
  const [busy, setBusy] = useState(false);
  if (!composing) return null;

  const list = Object.values(users)
    .filter((u) => u.id !== me && (u.display_name + ' ' + u.department).toLowerCase().includes(q.toLowerCase()))
    .sort((a, b) => a.display_name.localeCompare(b.display_name));
  function close() { setComposing(false); setQ(''); setTitle(''); setPicked([]); }

  async function submit() {
    setBusy(true);
    try {
      await start(picked, title, 'meeting');
      close();
    } catch { showToast(t('meet.err.start')); }
    finally { setBusy(false); }
  }

  return (
    <div id="dlg" role="presentation">
      <div className="dbox" role="dialog" aria-modal="true" aria-label={t('meet.new')}>
        <div className="dh"><b>{t('meet.new')}</b><span>{t('meet.newHint')}</span></div>
        <div className="form">
          <label htmlFor="mttl">{t('meet.title')}</label>
          <input id="mttl" className="inp" maxLength={200} placeholder={t('meet.titleDefault')} value={title} onChange={(e) => setTitle(e.target.value)} />
          <input className="inp" aria-label={t('list.search')} placeholder={t('list.search')} value={q} onChange={(e) => setQ(e.target.value)} />
          <div className="picker">
            {list.map((u) => (
              <label key={u.id}>
                <input type="checkbox" checked={picked.includes(u.id)} disabled={!picked.includes(u.id) && picked.length >= 11}
                  onChange={(e) => setPicked((p) => (e.target.checked ? [...p, u.id] : p.filter((x) => x !== u.id)))} />
                <UserAvatar u={u} status={presence[u.id]} />
                <span>{u.display_name}<br /><small style={{ color: 'var(--muted)' }}>{u.department}</small></span>
              </label>
            ))}
          </div>
          <small style={{ color: 'var(--muted)' }}>{t('meet.pickedCount', { n: picked.length })}</small>
        </div>
        <div className="bt">
          <button className="btn ghost" onClick={close}>{t('chat.cancel')}</button>
          <button className="btn" disabled={busy} onClick={() => void submit()}>{t('meet.start')}</button>
        </div>
      </div>
    </div>
  );
}
