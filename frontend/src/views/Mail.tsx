import { useState } from 'react';
import { mailAttachmentUrl, type MailAttachment, type MailMessage } from '../api/client';
import { t, dateLocale } from '../i18n';
import { useMail } from '../store/mail';

const kb = (n: number) => (n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} Mo` : `${Math.max(1, Math.round(n / 1024))} Ko`);
export const mailDate = (iso: string) => {
  const d = new Date(iso);
  const today = new Date();
  return d.toDateString() === today.toDateString()
    ? d.toLocaleTimeString(dateLocale(), { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleDateString(dateLocale(), { day: 'numeric', month: 'short' });
};
const full = (iso: string) => new Date(iso).toLocaleString(dateLocale(), { dateStyle: 'medium', timeStyle: 'short' });

function Attachment({ a }: { a: MailAttachment }) {
  async function download() {
    const url = await mailAttachmentUrl(a.id);
    const link = document.createElement('a');
    link.href = url; link.download = a.filename; link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
  }
  return <button type="button" className="mailatt" onClick={() => void download()}><b>{a.filename}</b><small>{kb(a.size)}</small></button>;
}

function MessageCard({ m, open, toggle }: { m: MailMessage; open: boolean; toggle: () => void }) {
  const { reply, forward, compose } = useMail();
  const names = (ps: { name: string }[]) => ps.map((p) => p.name).join(', ');
  return (
    <article className={'mailmsg' + (open ? ' open' : '')}>
      <header onClick={toggle} role="button" tabIndex={0} aria-expanded={open} onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(); } }}>
        <div className="who"><b>{m.mine ? t('mail.me') : m.from.name}</b>{m.draft && <span className="tkp warn">{t('mail.draftBadge')}</span>}
          {!open && <span className="pre">{m.body.replace(/\s+/g, ' ').slice(0, 90)}</span>}</div>
        <small>{full(m.date)}</small>
      </header>
      {open && (
        <div className="mbody">
          <div className="hdrs">
            <span>{t('mail.from')}</span><b>{m.from.name}</b>
            {m.to.length > 0 && <><span>{t('mail.to')}</span><b>{names(m.to)}</b></>}
            {m.cc.length > 0 && <><span>{t('mail.cc')}</span><b>{names(m.cc)}</b></>}
            {m.bcc.length > 0 && <><span>{t('mail.bcc')}</span><b>{names(m.bcc)}</b></>}
          </div>
          <div className="text">{m.body}</div>
          {m.attachments.length > 0 && <div className="mailatts" aria-label={t('mail.attachments')}>{m.attachments.map((a) => <Attachment key={a.id} a={a} />)}</div>}
          {!m.draft && (
            <div className="acts">
              <button className="btn" onClick={() => reply(m, false)}>{t('mail.reply')}</button>
              {(m.to.length + m.cc.length > 1 || (!m.mine && m.to.length + m.cc.length > 0)) && <button className="btn ghost" onClick={() => reply(m, true)}>{t('mail.replyAll')}</button>}
              <button className="btn ghost" onClick={() => forward(m)}>{t('mail.forward')}</button>
            </div>
          )}
          {m.draft && <div className="acts"><button className="btn" onClick={() => { compose({ id: m.id, to: m.to.map((p) => p.id), cc: m.cc.map((p) => p.id), bcc: m.bcc.map((p) => p.id), subject: m.subject, body: m.body, parent_id: m.parent_id ?? undefined, attachments: m.attachments, showCc: m.cc.length > 0, showBcc: m.bcc.length > 0 }); }}>{t('mail.edit')}</button></div>}
        </div>
      )}
    </article>
  );
}

export function Mail() {
  const { thread, act, compose, folder } = useMail();
  const [closed, setClosed] = useState<Record<number, boolean>>({});

  if (!thread) {
    return (
      <>
        <div className="top"><div className="tt"><b>{t('nav.mail')}</b><span>{t(`mail.folder.${folder}`)}</span></div></div>
        <div className="empty">
          <div>
            <p>{t('mail.empty')}</p>
            <p style={{ marginTop: 14 }}><button className="btn gold" onClick={() => compose()}>{t('mail.new')}</button></p>
          </div>
        </div>
      </>
    );
  }

  const ids = thread.messages.map((m) => m.id);
  const starred = thread.messages.some((m) => m.starred);
  const lastId = ids[ids.length - 1];
  const inTrash = thread.folder === 'trash';
  return (
    <>
      <div className="top">
        <div className="tt"><b>{thread.subject || t('mail.noSubject')}</b><span>{thread.messages.length > 1 ? t('mail.count', { n: thread.messages.length }) : thread.messages[0].from.name}</span></div>
        <div className="mailtools">
          {!inTrash && <button className="btn ghost" onClick={() => void act(starred ? 'unstar' : 'star', ids)} aria-pressed={starred}>{starred ? t('mail.unstar') : t('mail.star')}</button>}
          {!inTrash && <button className="btn ghost" onClick={() => void act('unread', ids)}>{t('mail.markUnread')}</button>}
          {!inTrash && thread.folder !== 'archive' && <button className="btn ghost" onClick={() => void act('archive', ids)}>{t('mail.archive')}</button>}
          {!inTrash && <button className="btn ghost" onClick={() => void act('trash', ids)}>{t('mail.delete')}</button>}
          {inTrash && <button className="btn ghost" onClick={() => void act('restore', ids)}>{t('mail.restore')}</button>}
          {inTrash && <button className="btn red" onClick={() => void act('purge', ids)}>{t('mail.purge')}</button>}
        </div>
      </div>
      <div className="prof" style={{ alignItems: 'flex-start' }}>
        <div className="help mailthread">
          {thread.messages.map((m) => {
            const open = closed[m.id] === undefined ? m.id === lastId || m.unread : !closed[m.id];
            return <MessageCard key={m.id} m={m} open={open} toggle={() => setClosed((c) => ({ ...c, [m.id]: open }))} />;
          })}
        </div>
      </div>
    </>
  );
}
