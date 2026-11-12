import { useState } from 'react';
import { useApp } from '../store/app';
import { useChat } from '../store/chat';
import { t } from '../i18n';
import { UserAvatar } from './Avatar';

export function NewChatDialog() {
  const { newChatOpen, setNewChat, users, presence, me, startDirect, createGroup } = useChat();
  const [q, setQ] = useState('');
  const [picked, setPicked] = useState<number[]>([]);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  if (!newChatOpen) return null;

  const list = Object.values(users)
    .filter((u) => u.id !== me && (u.display_name + u.department).toLowerCase().includes(q.toLowerCase()))
    .sort((a, b) => a.display_name.localeCompare(b.display_name));
  const group = picked.length > 1;
  const valid = picked.length > 0 && (!group || name.trim().length > 0);

  function close() { setNewChat(false); setPicked([]); setName(''); setQ(''); }

  async function submit() {
    setBusy(true);
    try {
      if (group) await createGroup(name.trim(), picked); else await startDirect(picked[0]);
      useApp.getState().setView('chat');
      close();
    } catch { useApp.getState().showToast(t('err.generic')); }
    finally { setBusy(false); }
  }

  return (
    <div id="dlg" role="presentation">
      <div className="dbox" role="dialog" aria-modal="true" aria-label={t('chat.new')}>
        <div className="dh"><b>{t('chat.new')}</b><span>{t('chat.pick')}</span></div>
        <div className="form">
          <input className="inp" autoFocus aria-label={t('list.search')} placeholder={t('list.search')} value={q} onChange={(e) => setQ(e.target.value)} />
          <div className="picker">
            {list.map((u) => (
              <label key={u.id}>
                <input type="checkbox" checked={picked.includes(u.id)}
                  onChange={(e) => setPicked((p) => (e.target.checked ? [...p, u.id] : p.filter((x) => x !== u.id)))} />
                <UserAvatar u={u} status={presence[u.id]} />
                <span>{u.display_name}<br /><small style={{ color: 'var(--muted)' }}>{u.department}</small></span>
              </label>
            ))}
          </div>
          {group && (
            <>
              <label htmlFor="gname">{t('chat.groupName')}</label>
              <input id="gname" className="inp" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
            </>
          )}
        </div>
        <div className="bt">
          <button className="btn ghost" onClick={close}>{t('chat.cancel')}</button>
          <button className="btn" disabled={!valid || busy} onClick={submit}>{t('chat.create')}</button>
        </div>
      </div>
    </div>
  );
}
