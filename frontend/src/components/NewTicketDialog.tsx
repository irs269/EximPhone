import { useEffect, useState, type FormEvent } from 'react';
import * as api from '../api/client';
import { ApiError, type TicketPriority, type TicketService } from '../api/client';
import { t } from '../i18n';
import { useApp } from '../store/app';
import { useTickets } from '../store/tickets';

const priorities: TicketPriority[] = ['low', 'normal', 'high', 'urgent'];

export function NewTicketDialog() {
  const { composing, setComposing, applied } = useTickets();
  const showToast = useApp((s) => s.showToast);
  const [services, setServices] = useState<TicketService[]>([]);
  const [dept, setDept] = useState('');
  const [prio, setPrio] = useState<TicketPriority>('normal');
  const [title, setTitle] = useState('');
  const [desc, setDesc] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    if (!composing) return;
    void api.ticketServices().then((r) => setServices(r.services)).catch(() => setServices([]));
  }, [composing]);
  if (!composing) return null;

  const svc = services.find((s) => String(s.id) === dept);
  const valid = !!svc && svc.heads.length > 0 && title.trim() !== '' && desc.trim() !== '';
  function close() { setComposing(false); setErr(''); setTitle(''); setDesc(''); setDept(''); setPrio('normal'); }

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!valid || !svc) return;
    setBusy(true); setErr('');
    try {
      const tk = await api.createTicket({ title: title.trim(), description: desc.trim(), priority: prio, department_id: svc.id });
      showToast(t('tk.sent', { service: svc.name }));
      close();
      await applied(tk);
    } catch (ex) {
      setErr(ex instanceof ApiError && ex.status === 409 ? t('tk.err.noHead') : t('err.generic'));
    } finally { setBusy(false); }
  }

  return (
    <div id="dlg" role="presentation" onClick={close}>
      <form className="dbox" role="dialog" aria-modal="true" aria-label={t('tk.new')} onClick={(e) => e.stopPropagation()} onSubmit={submit}>
        <div className="dh"><b>{t('tk.new')}</b></div>
        <div className="form" style={{ maxHeight: 440, overflow: 'auto' }}>
          <label htmlFor="tks">{t('tk.field.service')}</label>
          <select id="tks" className="inp" autoFocus required value={dept} onChange={(e) => setDept(e.target.value)}>
            <option value="">{t('tk.pickService')}</option>
            {services.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
          {svc && <small style={{ color: svc.heads.length ? 'var(--muted)' : 'var(--alert-dark)' }}>{svc.heads.length ? t('tk.heads', { names: svc.heads.join(', ') }) : t('tk.noHeads')}</small>}
          <label htmlFor="tkt">{t('tk.field.title')}</label>
          <input id="tkt" className="inp" required maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} />
          <label htmlFor="tkd">{t('tk.field.description')}</label>
          <textarea id="tkd" className="inp" required maxLength={5000} value={desc} onChange={(e) => setDesc(e.target.value)} />
          <label htmlFor="tkp">{t('tk.field.priority')}</label>
          <select id="tkp" className="inp" value={prio} onChange={(e) => setPrio(e.target.value as TicketPriority)}>
            {priorities.map((p) => <option key={p} value={p}>{t(`tk.prio.${p}`)}</option>)}
          </select>
          {err && <div role="alert" className="tknote bad">{err}</div>}
        </div>
        <div className="bt">
          <button type="button" className="btn ghost" onClick={close}>{t('out.cancel')}</button>
          <button type="submit" className="btn" disabled={busy || !valid}>{t('tk.send')}</button>
        </div>
      </form>
    </div>
  );
}
