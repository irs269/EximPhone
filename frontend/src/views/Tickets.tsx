import { useEffect, useState } from 'react';
import * as api from '../api/client';
import { ApiError, type Ticket } from '../api/client';
import { t, dateLocale } from '../i18n';
import { useTickets } from '../store/tickets';

export const statusClass: Record<Ticket['status'], string> = {
  pending: 'warn', approved: 'info', rejected: 'bad', assigned: 'info', in_progress: 'info', resolved: 'good', cancelled: 'off',
};
const when = (iso: string) => new Date(iso).toLocaleString(dateLocale(), { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

export const StatusPill = ({ s }: { s: Ticket['status'] }) => <span className={'tkp ' + statusClass[s]}>{t(`tk.status.${s}`)}</span>;
export const PrioPill = ({ p }: { p: Ticket['priority'] }) => <span className={'tkp prio ' + p}>{t(`tk.prio.${p}`)}</span>;

export function Tickets() {
  const { selected, summary, applied } = useTickets();
  const [busy, setBusy] = useState(false);
  const [decideNote, setDecideNote] = useState('');
  const [resolveNote, setResolveNote] = useState('');
  const [assignNote, setAssignNote] = useState('');
  const [comment, setComment] = useState('');
  const [assignee, setAssignee] = useState('');
  const [cands, setCands] = useState<{ id: number; display_name: string; extension: string }[]>([]);
  const [err, setErr] = useState('');
  const id = selected?.id;
  const canAssign = selected?.can.assign;

  // Reset the forms when another ticket is opened; load the assignable members for a head.
  useEffect(() => { setDecideNote(''); setResolveNote(''); setAssignNote(''); setComment(''); setAssignee(''); setErr(''); }, [id]);
  useEffect(() => {
    setCands([]);
    if (id && canAssign) void api.ticketCandidates(id).then((r) => setCands(r.candidates)).catch(() => setCands([]));
  }, [id, canAssign]);

  async function run(fn: () => Promise<Ticket | unknown>) {
    setBusy(true); setErr('');
    try {
      const r = await fn();
      if (r && typeof r === 'object' && 'can' in r) await applied(r as Ticket);
      else if (selected) await useTickets.getState().select(selected.id);
    } catch (e) {
      setErr(e instanceof ApiError && e.status !== 409 && e.status !== 403 ? e.message : t('tk.err.generic'));
    } finally { setBusy(false); }
  }

  if (!selected) {
    const counters = [['tk.toDecide', summary.to_decide], ['tk.toAssign', summary.to_assign], ['tk.toDo', summary.to_do], ['tk.mineOpen', summary.mine_open]] as const;
    return (
      <>
        <div className="top"><div className="tt"><b>{t('nav.tickets')}</b><span>{t('tk.sub')}</span></div></div>
        <div className="empty">
          <div>
            <p>{t('tk.empty')}</p>
            <div className="kp" style={{ marginTop: 18 }}>
              {counters.map(([k, v]) => (
                <div className="card" key={k}><small>{t(k)}</small><b>{v}</b></div>
              ))}
            </div>
          </div>
        </div>
      </>
    );
  }

  const tk = selected;
  const c = tk.can;
  return (
    <>
      <div className="top"><div className="tt"><b>#{tk.id} · {tk.title}</b><span>{tk.department} · {tk.requester}</span></div><StatusPill s={tk.status} /></div>
      <div className="prof" style={{ alignItems: 'flex-start' }}>
        <div className="help">
          <section className="card">
            <div className="kv" style={{ marginBottom: 14 }}>
              <span>{t('tk.service')}</span><b>{tk.department}</b>
              <span>{t('tk.requester')}</span><b>{tk.requester}</b>
              <span>{t('tk.field.priority')}</span><b><PrioPill p={tk.priority} /></b>
              <span>{t('tk.assignee')}</span><b>{tk.assignee || t('tk.notAssigned')}</b>
              <span>{t('tk.createdAt')}</span><b>{when(tk.created_at)}</b>
            </div>
            <p style={{ whiteSpace: 'pre-wrap', lineHeight: 1.55 }}>{tk.description}</p>
            {tk.decided_by && (tk.status === 'rejected' || tk.decision_comment) && (
              <div className={'tknote ' + (tk.status === 'rejected' ? 'bad' : '')}>
                <b>{t('tk.decision', { name: tk.decided_by })}</b>{tk.decision_comment && <p>{tk.decision_comment}</p>}
              </div>
            )}
            {tk.resolution && <div className="tknote good"><b>{t('tk.resolution')}</b><p>{tk.resolution}</p></div>}
          </section>

          {err && <div role="alert" className="tknote bad">{err}</div>}

          {c.decide && (
            <section className="card">
              <h4>{t('tk.toDecide')}</h4>
              <textarea className="inp" aria-label={t('tk.decideComment')} placeholder={t('tk.decideComment')} maxLength={2000} value={decideNote} onChange={(e) => setDecideNote(e.target.value)} />
              <div className="acts" style={{ marginTop: 10 }}>
                <button className="btn" disabled={busy} onClick={() => void run(() => api.decideTicket(tk.id, true, decideNote.trim()))}>{t('tk.approve')}</button>
                <button className="btn red" disabled={busy}
                  onClick={() => { if (!decideNote.trim()) { setErr(t('tk.rejectNeeds')); return; } void run(() => api.decideTicket(tk.id, false, decideNote.trim())); }}>{t('tk.reject')}</button>
              </div>
            </section>
          )}

          {c.assign && (
            <section className="card">
              <h4>{t('tk.assignTo')}</h4>
              <select className="inp" aria-label={t('tk.assignTo')} value={assignee} onChange={(e) => setAssignee(e.target.value)}>
                <option value="">{t('tk.pickMember')}</option>
                {cands.filter((m) => m.id !== tk.assignee_id).map((m) => <option key={m.id} value={m.id}>{m.display_name}{m.extension ? ` · ${m.extension}` : ''}</option>)}
              </select>
              <input className="inp" style={{ marginTop: 8 }} aria-label={t('tk.assignComment')} placeholder={t('tk.assignComment')} maxLength={2000} value={assignNote} onChange={(e) => setAssignNote(e.target.value)} />
              <div className="acts" style={{ marginTop: 10 }}>
                <button className="btn gold" disabled={busy || !assignee} onClick={() => void run(() => api.assignTicket(tk.id, Number(assignee), assignNote.trim()))}>
                  {tk.assignee_id ? t('tk.reassign') : t('tk.assign')}
                </button>
              </div>
            </section>
          )}

          {(c.start || c.resolve) && (
            <section className="card">
              {c.resolve && <textarea className="inp" aria-label={t('tk.resolutionComment')} placeholder={t('tk.resolutionComment')} maxLength={2000} value={resolveNote} onChange={(e) => setResolveNote(e.target.value)} />}
              <div className="acts" style={{ marginTop: c.resolve ? 10 : 0 }}>
                {c.start && <button className="btn" disabled={busy} onClick={() => void run(() => api.startTicket(tk.id))}>{t('tk.start')}</button>}
                {c.resolve && <button className="btn gold" disabled={busy} onClick={() => void run(() => api.resolveTicket(tk.id, resolveNote.trim()))}>{t('tk.resolve')}</button>}
              </div>
            </section>
          )}

          {c.cancel && (
            <div><button className="btn ghost" disabled={busy} onClick={() => void run(() => api.cancelTicket(tk.id))}>{t('tk.cancel')}</button></div>
          )}

          <section className="card">
            <h4>{t('tk.history')}</h4>
            <ol className="tkh">
              {(tk.events ?? []).map((e, i) => (
                <li key={i}>
                  <div><b>{e.actor}</b> {t(`tk.ev.${e.kind}` as 'tk.ev.created')}<small>{when(e.created_at)}</small></div>
                  {e.comment && <p>{e.comment}</p>}
                </li>
              ))}
            </ol>
            <form className="tkc" onSubmit={(e) => { e.preventDefault(); const text = comment.trim(); if (text) { setComment(''); void run(() => api.commentTicket(tk.id, text)); } }}>
              <input className="inp" aria-label={t('tk.comment')} placeholder={t('tk.comment')} maxLength={2000} value={comment} onChange={(e) => setComment(e.target.value)} />
              <button className="btn" type="submit" disabled={busy || !comment.trim()}>{t('tk.commentSend')}</button>
            </form>
          </section>
        </div>
      </div>
    </>
  );
}
