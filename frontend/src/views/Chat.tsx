import { useEffect, useRef, useState } from 'react';
import { useApp } from '../store/app';
import { t } from '../i18n';
import { Icon } from '../components/Icon';
import { Avatar, GroupAvatar, lineLabel } from '../components/Avatar';
import { person } from '../data/mock';

export function Chat() {
  const { chats, cur, openChat, send, startCall, setCur, setView, showToast } = useApp();
  const chat = chats.find((c) => c.id === cur) ?? chats[0];
  const [text, setText] = useState('');
  const end = useRef<HTMLDivElement>(null);

  useEffect(() => { if (chat && chat.id !== cur) openChat(chat.id); }, [chat, cur, openChat]);
  useEffect(() => { end.current?.scrollIntoView(); }, [chat?.msgs.length, chat?.id]);
  if (!chat) return null;

  const p = chat.person ? person(chat.person) : null;
  const sub = p
    ? `${p.role} · Poste ${p.ext} · ${lineLabel[p.line]}`
    : [...(chat.members ?? []).map((id) => person(id).name), 'vous'].join(', ');

  return (
    <>
      <div className="mock" role="note">{t('mock.banner')}</div>
      <div className="top">
        {p ? <Avatar p={p} /> : <GroupAvatar />}
        <div className="tt"><b>{chat.group ?? p!.name}</b><span>{sub}</span></div>
        {p && <button className="ib" aria-label="Appeler" onClick={() => startCall(p)}><Icon name="call" /></button>}
        <button className="ib" aria-label="Informations"
          onClick={() => (p ? (setCur('p' + p.id), setView('dir')) : showToast('Groupe chiffré · ' + ((chat.members?.length ?? 0) + 1) + ' membres'))}>
          <Icon name="grid" />
        </button>
      </div>
      <div className="msgs" role="log" aria-live="polite">
        <div className="day">Aujourd'hui</div>
        {chat.msgs.map((m, i) => {
          const me = m.from === 'me';
          const who = chat.group && !me && typeof m.from === 'number' ? person(m.from) : null;
          return (
            <div key={i} className={'m' + (me ? ' me' : '')}>
              {who && <div className="who">{who.name}</div>}
              {m.text}<small>{m.time}{me ? ' ✓✓' : ''}</small>
            </div>
          );
        })}
        <div ref={end} />
      </div>
      <form className="compose" onSubmit={(e) => { e.preventDefault(); const v = text.trim(); if (v) { send(chat.id, v); setText(''); } }}>
        <input aria-label="Écrire un message" placeholder="Écrire un message…" autoComplete="off" value={text} onChange={(e) => setText(e.target.value)} />
        <button className="round" style={{ width: 44, height: 44, background: 'var(--navy)' }} aria-label="Envoyer"><Icon name="send" /></button>
      </form>
      <div className="lock"><Icon name="lock" size={11} /> Messages chiffrés en transit · archivés sur les serveurs Exim Bank</div>
    </>
  );
}
