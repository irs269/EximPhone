import { useEffect, useRef, useState } from 'react';
import { useApp } from '../store/app';
import { t } from '../i18n';
import { durMin, hm } from '../lib';

function Modal({ children, label }: { children: React.ReactNode; label: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => { ref.current?.querySelector<HTMLElement>('button, textarea')?.focus(); }, []);
  return (
    <div id="dlg" role="presentation">
      <div className="dbox" role="dialog" aria-modal="true" aria-label={label} ref={ref}>{children}</div>
    </div>
  );
}

export function Dialogs() {
  const { dialog, user, today, closeDialog } = useApp();
  if (!dialog) return null;
  const first = user?.display_name.split(' ')[0] ?? '';

  if (dialog.kind === 'arrival' && today) {
    const late = today.arrival === 'late';
    return (
      <Modal label={t('arr.title')}>
        <div className="dh"><div className="ck" aria-hidden>✓</div><b>{t('arr.title')}</b><span>{t('arr.hello', { name: first })}</span></div>
        <div className="bd">
          <div className="kv"><span>{t('arr.time')}</span><b>{hm(today.clock_in)}</b></div>
          <div className="kv"><span>{t('arr.planned')}</span>{t('arr.plannedVal')}</div>
          <div className="kv" style={{ border: 0 }}><span>{t('arr.status')}</span><b className={late ? 'late' : 'ok'}>{late ? t('st.late') : t('st.onTime')}</b></div>
          {dialog.resumed && <div className="mock">{t('arr.resumed')}</div>}
        </div>
        <div className="bt"><button className="btn" onClick={closeDialog}>{t('arr.start')}</button></div>
      </Modal>
    );
  }
  if (dialog.kind === 'logout') return <LogoutDialog />;
  if (dialog.kind === 'bye') {
    return (
      <Modal label={t('bye.title', { time: hm(dialog.time) })}>
        <div className="dh"><div className="ck" aria-hidden>👋</div><b>{t('bye.title', { time: hm(dialog.time) })}</b><span>{t('bye.sub', { name: dialog.name })}</span></div>
        <div className="bd"><div className="kv" style={{ border: 0 }}><span>{t('bye.presence')}</span><b>{durMin(dialog.durationMin)}</b></div></div>
        <div className="bt"><button className="btn" onClick={closeDialog}>{t('bye.close')}</button></div>
      </Modal>
    );
  }
  return null;
}

function LogoutDialog() {
  const { dialog, closeDialog, confirmLogout } = useApp();
  const [reason, setReason] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  if (dialog?.kind !== 'logout') return null;
  const s = dialog.summary;
  const needReason = s.early && !reason.trim();

  async function submit() {
    setBusy(true);
    const e = await confirmLogout(reason.trim());
    setBusy(false);
    if (e) setErr(e);
  }

  return (
    <Modal label={t('out.title')}>
      <div className="dh" style={{ background: 'linear-gradient(150deg, var(--alert-dark), var(--alert))' }}>
        <div className="ck" style={{ background: 'var(--surface)', color: 'var(--alert)' }} aria-hidden>⏻</div>
        <b>{t('out.title')}</b><span>{t('out.sub')}</span>
      </div>
      <div className="bd">
        <div className="kv"><span>{t('out.arrival')}</span><b>{hm(s.clock_in)}</b></div>
        <div className="kv"><span>{t('out.departure')}</span><b>{hm(s.clock_out)}</b></div>
        <div className="kv" style={{ border: 0 }}><span>{t('out.presence')}</span><b>{durMin(s.duration_min)}</b></div>
        {s.early && (
          <div style={{ textAlign: 'left' }}>
            <div style={{ background: 'var(--warn-bg)', color: 'var(--warn-bg-text)', borderRadius: 8, padding: 10, fontSize: 12.5, marginTop: 8 }}>{t('out.earlyWarn')}</div>
            <label htmlFor="reason">{t('out.reason')}</label>
            <textarea id="reason" className="inp" value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </div>
        )}
        {err && <div className="err" role="alert">{err}</div>}
      </div>
      <div className="bt">
        <button className="btn ghost" onClick={closeDialog}>{t('out.cancel')}</button>
        <button className="btn red" disabled={needReason || busy} onClick={submit}>{t('out.confirm')}</button>
      </div>
    </Modal>
  );
}
