import { useEffect, useRef, useState, type ChangeEvent } from 'react';
import { ApiError, mailUpload } from '../api/client';
import { t } from '../i18n';
import { useChat } from '../store/chat';
import { useMail } from '../store/mail';

type Field = 'to' | 'cc' | 'bcc';
const EMAIL = /^[^\s@<>,;]+@[^\s@<>,;]+\.[^\s@<>,;]+$/;

/** Recipient field: chips for the chosen people and an autocomplete over the directory. */
function Recipients({ field, label }: { field: Field; label: string }) {
  const c = useMail((s) => s.composer);
  const edit = useMail((s) => s.edit);
  const users = useChat((s) => s.users);
  const [q, setQ] = useState('');
  const [focus, setFocus] = useState(false);
  if (!c) return null;
  const chosen = c[field];
  const extField = (field + '_ext') as 'to_ext' | 'cc_ext' | 'bcc_ext';
  const chosenExt = c[extField];
  const taken = new Set([...c.to, ...c.cc, ...c.bcc]);
  const typed = q.trim().replace(/[,;]$/, '');
  const canAddAddress = EMAIL.test(typed) && ![...c.to_ext, ...c.cc_ext, ...c.bcc_ext].includes(typed.toLowerCase());
  const matches = q.trim() === '' ? [] : Object.values(users)
    .filter((u) => !taken.has(u.id) && (u.display_name + ' ' + u.email + ' ' + u.department).toLowerCase().includes(q.trim().toLowerCase()))
    .sort((a, b) => a.display_name.localeCompare(b.display_name)).slice(0, 6);
  const add = (id: number) => { edit({ [field]: [...chosen, id] }); setQ(''); };
  const addAddress = () => { if (canAddAddress) { edit({ [extField]: [...chosenExt, typed.toLowerCase()] }); setQ(''); } };
  return (
    <div className="mrow">
      <label htmlFor={'mr-' + field}>{label}</label>
      <div className="chips" onClick={() => document.getElementById('mr-' + field)?.focus()}>
        {chosenExt.map((addr) => (
          <span key={addr} className="chip ext" title={t('mail.external')}>{addr}
            <button type="button" aria-label={t('mail.removeAttachment') + ' ' + addr} onClick={(e) => { e.stopPropagation(); edit({ [extField]: chosenExt.filter((x) => x !== addr) }); }}>×</button></span>
        ))}
        {chosen.map((id) => (
          <span key={id} className="chip">{users[id]?.display_name ?? '#' + id}
            <button type="button" aria-label={t('mail.removeAttachment') + ' ' + (users[id]?.display_name ?? '')} onClick={(e) => { e.stopPropagation(); edit({ [field]: chosen.filter((x) => x !== id) }); }}>×</button></span>
        ))}
        <input id={'mr-' + field} value={q} autoComplete="off" placeholder={chosen.length + chosenExt.length ? '' : t('mail.addRecipient')}
          onChange={(e) => setQ(e.target.value)} onFocus={() => setFocus(true)} onBlur={() => window.setTimeout(() => { setFocus(false); addAddress(); }, 150)}
          onKeyDown={(e) => {
            if ((e.key === 'Enter' || e.key === ',' || e.key === ';') && matches[0]) { e.preventDefault(); add(matches[0].id); }
            else if ((e.key === 'Enter' || e.key === ',' || e.key === ';' || e.key === ' ') && canAddAddress) { e.preventDefault(); addAddress(); }
            if (e.key === 'Backspace' && q === '') { if (chosenExt.length) edit({ [extField]: chosenExt.slice(0, -1) }); else if (chosen.length) edit({ [field]: chosen.slice(0, -1) }); }
          }} />
        {focus && (matches.length > 0 || canAddAddress) && (
          <ul className="sugg" role="listbox">
            {matches.map((u) => (
              <li key={u.id} role="option" aria-selected="false" onMouseDown={(e) => { e.preventDefault(); add(u.id); }}>
                <b>{u.display_name}</b><small>{u.department} · {u.email}</small>
              </li>
            ))}
            {canAddAddress && <li role="option" aria-selected="false" onMouseDown={(e) => { e.preventDefault(); addAddress(); }}><b>{t('mail.addAddress', { address: typed })}</b><small>{t('mail.external')}</small></li>}
          </ul>
        )}
      </div>
    </div>
  );
}

export function MailComposer() {
  const c = useMail((s) => s.composer);
  const { edit, send, saveDraft, discard, closeComposer } = useMail();
  const fileRef = useRef<HTMLInputElement>(null);
  const [uploadErr, setUploadErr] = useState('');
  const [uploading, setUploading] = useState(false);

  // Autosave: a draft that changed is saved 8 seconds after the last keystroke.
  const dirty = !!c?.dirty && !c.busy;
  const stamp = c ? c.subject + '|' + c.body + '|' + c.to + c.cc + c.bcc + c.attachments.length : '';
  useEffect(() => {
    if (!dirty) return;
    const id = window.setTimeout(() => { void saveDraft(true); }, 8000);
    return () => window.clearTimeout(id);
  }, [dirty, stamp, saveDraft]);

  if (!c) return null;

  async function onFiles(e: ChangeEvent<HTMLInputElement>) {
    const files = Array.from(e.target.files ?? []);
    e.target.value = '';
    setUploadErr(''); setUploading(true);
    try {
      for (const f of files) {
        const a = await mailUpload(f);
        useMail.setState((s) => (s.composer ? { composer: { ...s.composer, attachments: [...s.composer.attachments, a], dirty: true } } : s));
      }
    } catch (ex) {
      setUploadErr(ex instanceof ApiError ? t('mail.err.upload') : t('mail.err.generic'));
    } finally { setUploading(false); }
  }

  return (
    <div id="dlg" role="presentation">
      <form className="dbox mailc" role="dialog" aria-modal="true" aria-label={t('mail.new')}
        onSubmit={(e) => { e.preventDefault(); void send(); }}>
        <div className="dh mailc-h"><b>{c.id && c.subject ? c.subject : t('mail.new')}</b>
          <button type="button" className="x" aria-label={t('mail.close')} onClick={() => void closeComposer()}>×</button></div>
        <div className="form mailc-b">
          <Recipients field="to" label={t('mail.to')} />
          {!c.showCc && !c.showBcc && c.cc.length + c.cc_ext.length === 0 && c.bcc.length + c.bcc_ext.length === 0 && (
            <div className="mcc"><button type="button" className="lnk" onClick={() => useMail.setState((s) => (s.composer ? { composer: { ...s.composer, showCc: true, showBcc: true } } : s))}>{t('mail.cc')} / {t('mail.bcc')}</button></div>
          )}
          {(c.showCc || c.cc.length + c.cc_ext.length > 0) && <Recipients field="cc" label={t('mail.cc')} />}
          {(c.showBcc || c.bcc.length + c.bcc_ext.length > 0) && <Recipients field="bcc" label={t('mail.bcc')} />}
          <div className="mrow">
            <label htmlFor="msub">{t('mail.subject')}</label>
            <input id="msub" className="inp" maxLength={300} value={c.subject} onChange={(e) => edit({ subject: e.target.value })} />
          </div>
          <label htmlFor="mbody" className="sr">{t('mail.body')}</label>
          <textarea id="mbody" className="inp mailtxt" maxLength={100000} value={c.body} onChange={(e) => edit({ body: e.target.value })} autoFocus={c.to.length + c.to_ext.length > 0} />
          {c.attachments.length > 0 && (
            <ul className="mailatts edit" aria-label={t('mail.attachments')}>
              {c.attachments.map((a) => (
                <li key={a.id} className="mailatt"><b>{a.filename}</b><small>{Math.max(1, Math.round(a.size / 1024))} Ko</small>
                  <button type="button" aria-label={t('mail.removeAttachment') + ' ' + a.filename} onClick={() => edit({ attachments: c.attachments.filter((x) => x.id !== a.id) })}>×</button></li>
              ))}
            </ul>
          )}
          {(c.error || uploadErr) && <div role="alert" className="tknote bad">{c.error || uploadErr}</div>}
        </div>
        <div className="bt mailc-f">
          <input ref={fileRef} type="file" multiple hidden onChange={(e) => void onFiles(e)} />
          <button type="button" className="btn ghost" disabled={uploading} onClick={() => fileRef.current?.click()}>{t('mail.attach')}</button>
          <span style={{ flex: 1 }} />
          <button type="button" className="btn ghost" onClick={() => void discard()}>{t('mail.discard')}</button>
          <button type="button" className="btn ghost" disabled={c.busy} onClick={() => void saveDraft(false)}>{t('mail.saveDraft')}</button>
          <button type="submit" className="btn" disabled={c.busy || uploading}>{t('mail.send')}</button>
        </div>
      </form>
    </div>
  );
}
