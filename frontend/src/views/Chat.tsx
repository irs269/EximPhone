import { useEffect, useRef, useState } from 'react';
import { attachmentUrl, ApiError, uploadAttachment, type Message } from '../api/client';
import { useApp } from '../store/app';
import { convTitle, useChat } from '../store/chat';
import { t } from '../i18n';
import { Icon } from '../components/Icon';
import { GroupAvatar, UserAvatar, toPerson } from '../components/Avatar';
import { hm } from '../lib';

const ticks: Record<string, string> = { sent: '✓', delivered: '✓✓', read: '✓✓' };
const kb = (n: number) => (n > 1048576 ? (n / 1048576).toFixed(1) + ' Mo' : Math.max(1, Math.round(n / 1024)) + ' Ko');

function AttachmentView({ m }: { m: Message }) {
  const a = m.attachment!;
  const [src, setSrc] = useState('');
  const isImage = a.mime.startsWith('image/');
  useEffect(() => {
    if (!isImage) return;
    let url = '';
    void attachmentUrl(a.id).then((u) => { url = u; setSrc(u); }).catch(() => undefined);
    return () => { if (url) URL.revokeObjectURL(url); };
  }, [a.id, isImage]);

  async function download() {
    const url = await attachmentUrl(a.id);
    const link = document.createElement('a');
    link.href = url; link.download = a.filename; link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 5000);
  }
  return (
    <div className="att-box">
      {isImage && src && <img src={src} alt={a.filename} className="att-img" />}
      <button type="button" className="att-file" onClick={download}>
        <b>{a.filename}</b><small>{kb(a.size)} · {t('chat.download')}</small>
      </button>
    </div>
  );
}

export function Chat() {
  const { convs, users, msgs, hasMore, typing, active, me, send, remove, loadOlder, notifyTyping, setNewChat } = useChat();
  const { showToast } = useApp();
  const conv = convs.find((c) => c.id === active);
  const list = active ? msgs[active] ?? [] : [];
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const end = useRef<HTMLDivElement>(null);
  const file = useRef<HTMLInputElement>(null);

  useEffect(() => { end.current?.scrollIntoView(); }, [list.length, active]);
  useEffect(() => { setText(''); }, [active]);

  if (!conv || !active) {
    return (
      <div className="empty">
        <div>
          <p>{t('chat.empty')}</p>
          <button className="btn" style={{ marginTop: 14 }} onClick={() => setNewChat(true)}>{t('chat.new')}</button>
        </div>
      </div>
    );
  }

  const other = conv.kind === 'direct' ? users[conv.members.find((id) => id !== me) ?? 0] : undefined;
  const title = convTitle(conv, users, me);
  const sub = other
    ? `${other.department} · Poste ${other.extension}`
    : t('chat.members', { n: conv.members.length });
  const typer = typing[active] ? users[typing[active]]?.display_name.split(' ')[0] : '';

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const v = text.trim();
    if (!v || busy) return;
    setText('');
    await send(active!, v);
  }

  async function pick(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    e.target.value = '';
    if (!f) return;
    setBusy(true);
    try {
      const att = await uploadAttachment(active!, f);
      await send(active!, text.trim(), att.id);
      setText('');
    } catch (err) {
      showToast(err instanceof ApiError && err.status >= 400 && err.status < 500 ? t('chat.fileError') : t('err.generic'));
    } finally { setBusy(false); }
  }

  return (
    <>
      <div className="top">
        {other ? <UserAvatar u={other} status={useChat.getState().presence[other.id]} /> : <GroupAvatar />}
        <div className="tt"><b>{title}</b><span>{sub}</span></div>
        {other && other.extension && <button className="ib" aria-label="Appeler" onClick={() => useApp.getState().startCall(toPerson(other))}><Icon name="call" /></button>}
      </div>
      <div className="msgs" role="log" aria-live="polite">
        {hasMore[active] && <button className="btn ghost" style={{ alignSelf: 'center' }} onClick={() => loadOlder(active)}>{t('chat.older')}</button>}
        {list.map((m) => {
          const mine = m.sender_id === me;
          const who = conv.kind === 'group' && !mine ? users[m.sender_id]?.display_name : '';
          return (
            <div key={m.id} className={'m' + (mine ? ' me' : '') + (m.deleted ? ' del' : '')}>
              {who && <div className="who">{who}</div>}
              {m.deleted ? <i>{t('chat.deleted')}</i> : (<>
                {m.attachment && <AttachmentView m={m} />}
                {m.body}
              </>)}
              <small>
                {hm(m.created_at)}
                {mine && m.status && !m.deleted && <span className={'tick ' + m.status} aria-label={m.status}> {ticks[m.status]}</span>}
                {mine && !m.deleted && <button className="msg-del" aria-label={t('chat.delete')} title={t('chat.delete')} onClick={() => remove(active, m.id)}>×</button>}
              </small>
            </div>
          );
        })}
        <div ref={end} />
      </div>
      <div className="typing" aria-live="polite">{typer ? t('chat.typing', { name: typer }) : ''}</div>
      <form className="compose" onSubmit={submit}>
        <input ref={file} type="file" hidden accept="application/pdf,image/png,image/jpeg" onChange={pick} />
        <button type="button" className="ib" aria-label={t('chat.attach')} title={t('chat.attach')} onClick={() => file.current?.click()}>📎</button>
        <input aria-label={t('chat.placeholder')} placeholder={t('chat.placeholder')} autoComplete="off" maxLength={4000} value={text}
          onChange={(e) => { setText(e.target.value); notifyTyping(active); }} />
        <button className="round" style={{ width: 44, height: 44, background: 'var(--navy)' }} aria-label={t('chat.send')} disabled={busy}><Icon name="send" /></button>
      </form>
      <div className="lock"><Icon name="lock" size={11} /> {t('chat.lock')}</div>
    </>
  );
}
