import { useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { downloadExport, hrAttendance, hrCorrect, hrEarly, hrOverview, hrValidate, type Bucket, type DayRow } from '../api/client';
import { useApp } from '../store/app';
import { t } from '../i18n';
import { durMin, hm } from '../lib';

const statusLabel = { present: 'st.present', late: 'st.late', absent: 'team.absent' } as const;
const statusClass = { present: 'ok', late: 'late', absent: 'ko' } as const;

function Breakdown({ title, rows }: { title: string; rows: Bucket[] }) {
  return (
    <div className="card">
      <h4>{title}</h4>
      <table>
        <thead><tr><th /><th>{t('st.present')}</th><th>{t('st.late')}</th><th>{t('team.absent')}</th></tr></thead>
        <tbody>{rows.map((b) => <tr key={b.name}><td><b>{b.name || '—'}</b></td><td>{b.present}</td><td className="late">{b.late}</td><td className="ko">{b.absent}</td></tr>)}</tbody>
      </table>
    </div>
  );
}

function CorrectDialog({ day, onClose, onDone }: { day: DayRow; onClose: () => void; onDone: () => void }) {
  const toLocal = (iso: string | null) => (iso ? new Date(new Date(iso).getTime() - new Date().getTimezoneOffset() * 60000).toISOString().slice(0, 16) : '');
  const [cin, setCin] = useState(toLocal(day.clock_in));
  const [cout, setCout] = useState(toLocal(day.clock_out));
  const [why, setWhy] = useState('');
  const [err, setErr] = useState('');
  async function submit() {
    try {
      await hrCorrect(day.id, { clock_in: new Date(cin).toISOString(), clock_out: cout ? new Date(cout).toISOString() : undefined, justification: why });
      onDone();
    } catch { setErr(t('team.correctError')); }
  }
  return (
    <div id="dlg" role="presentation">
      <div className="dbox" role="dialog" aria-modal="true" aria-label={t('team.correct')}>
        <div className="dh"><b>{t('team.correct')}</b><span>{day.display_name} · {day.work_date}</span></div>
        <div className="form">
          <label htmlFor="ci">{t('att.in')}</label><input id="ci" className="inp" type="datetime-local" value={cin} onChange={(e) => setCin(e.target.value)} />
          <label htmlFor="co">{t('att.out')}</label><input id="co" className="inp" type="datetime-local" value={cout} onChange={(e) => setCout(e.target.value)} />
          <label htmlFor="cj">{t('team.justification')}</label>
          <textarea id="cj" className="inp" maxLength={500} value={why} onChange={(e) => setWhy(e.target.value)} />
          {err && <div className="err" role="alert">{err}</div>}
        </div>
        <div className="bt">
          <button className="btn ghost" onClick={onClose}>{t('out.cancel')}</button>
          <button className="btn" disabled={why.trim().length < 5 || !cin || !cout} onClick={submit}>{t('team.save')}</button>
        </div>
      </div>
    </div>
  );
}

export function Team() {
  const roles = useApp((s) => s.roles);
  const showToast = useApp((s) => s.showToast);
  const isHR = roles.includes('hr');
  const qc = useQueryClient();
  const today = new Date().toISOString().slice(0, 10);
  const [date, setDate] = useState(today);
  const [filter, setFilter] = useState('');
  const [status, setStatus] = useState('');
  const [month, setMonth] = useState(today.slice(0, 7));
  const [comments, setComments] = useState<Record<number, string>>({});
  const [fix, setFix] = useState<DayRow | null>(null);

  const overview = useQuery({ queryKey: ['overview', date], queryFn: () => hrOverview(date), refetchInterval: 30000 });
  const early = useQuery({ queryKey: ['early'], queryFn: hrEarly });
  const history = useQuery({ queryKey: ['hist', status], queryFn: () => hrAttendance({ status }), enabled: isHR });
  const refresh = useCallback(() => { void qc.invalidateQueries(); }, [qc]);
  useEffect(() => { setComments({}); }, [early.data]);

  const rows = useMemo(() => (overview.data?.rows ?? []).filter((r) => (!filter || (r.display_name + r.department + r.site).toLowerCase().includes(filter.toLowerCase()))), [overview.data, filter]);
  const o = overview.data;

  async function verdict(id: number, approved: boolean) {
    try { await hrValidate(id, approved, comments[id] ?? ''); showToast(t('team.validated')); refresh(); }
    catch { showToast(t('err.generic')); }
  }
  async function exportFile(format: 'csv' | 'xlsx') {
    try { await downloadExport(month, format); } catch { showToast(t('err.generic')); }
  }

  return (
    <>
      <div className="top">
        <div className="tt"><b>{isHR ? t('team.titleHR') : t('team.titleManager')}</b><span>{t('team.sub')}</span></div>
        <input className="inp" style={{ width: 160 }} type="date" aria-label={t('att.date')} value={date} max={today} onChange={(e) => setDate(e.target.value || today)} />
      </div>
      <div className="att">
        {o && (
          <div className="kp">
            <div className="card"><small>{t('team.presentNow')}</small><b className="ok">{o.totals.present + o.totals.late}</b></div>
            <div className="card"><small>{t('st.late')}</small><b className="late">{o.totals.late}</b></div>
            <div className="card"><small>{t('team.absent')}</small><b className="ko">{o.totals.absent}</b></div>
            <div className="card"><small>{t('team.total')}</small><b>{o.rows.length}</b></div>
          </div>
        )}
        {o?.weekend && <div className="mock">{t('team.weekend')}</div>}
        {o && (
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
            <Breakdown title={t('team.bySite')} rows={o.by_site} />
            <Breakdown title={t('team.byDept')} rows={o.by_department} />
          </div>
        )}

        <div className="card">
          <h4>{t('team.employees')}
            <input className="inp" style={{ width: 220 }} aria-label={t('list.search')} placeholder={t('list.search')} value={filter} onChange={(e) => setFilter(e.target.value)} />
          </h4>
          <table>
            <thead><tr><th>{t('team.name')}</th><th>{t('team.dept')}</th><th>{t('team.site')}</th><th>{t('att.in')}</th><th>{t('att.out')}</th><th>{t('att.status')}</th></tr></thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.user_id}>
                  <td><b>{r.display_name}</b></td><td>{r.department}</td><td>{r.site}</td>
                  <td>{r.clock_in ? hm(r.clock_in) : '—'}</td><td>{r.clock_out ? hm(r.clock_out) : r.clock_in ? t('att.running') : '—'}</td>
                  <td className={statusClass[r.status]}>{t(statusLabel[r.status])}{r.departure === 'not_clocked' ? ` · ${t('st.notClocked')}` : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="card">
          <h4>{t('team.earlyQueue')} ({early.data?.days.length ?? 0})</h4>
          {!early.data?.days.length && <div style={{ color: 'var(--muted)' }}>{t('team.noneToValidate')}</div>}
          <table>
            <tbody>
              {early.data?.days.slice(0, 15).map((d) => (
                <tr key={d.id}>
                  <td><b>{d.display_name}</b><br /><small style={{ color: 'var(--muted)' }}>{d.work_date} · {d.clock_out ? hm(d.clock_out) : ''}</small></td>
                  <td>{d.early_reason}</td>
                  <td><input className="inp" aria-label={t('team.comment')} placeholder={t('team.comment')} value={comments[d.id] ?? ''} onChange={(e) => setComments({ ...comments, [d.id]: e.target.value })} /></td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <button className="ack done" onClick={() => void verdict(d.id, true)}>{t('team.approve')}</button>{' '}
                    <button className="ack" onClick={() => void verdict(d.id, false)}>{t('team.reject')}</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {isHR && (
          <>
            <div className="card">
              <h4>{t('team.export')}</h4>
              <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
                <input className="inp" style={{ width: 180 }} type="month" aria-label={t('team.month')} value={month} onChange={(e) => setMonth(e.target.value)} />
                <button className="btn" onClick={() => void exportFile('xlsx')}>Excel (.xlsx)</button>
                <button className="btn gold" onClick={() => void exportFile('csv')}>CSV</button>
              </div>
            </div>
            <div className="card">
              <h4>{t('team.history')}
                <select className="inp" style={{ width: 220 }} aria-label={t('att.status')} value={status} onChange={(e) => setStatus(e.target.value)}>
                  <option value="">{t('team.allStatuses')}</option><option value="late">{t('st.late')}</option>
                  <option value="early">{t('st.early')}</option><option value="not_clocked">{t('st.notClocked')}</option>
                </select>
              </h4>
              <table>
                <thead><tr><th>{t('att.date')}</th><th>{t('team.name')}</th><th>{t('att.in')}</th><th>{t('att.out')}</th><th>{t('att.duration')}</th><th>{t('att.status')}</th><th /></tr></thead>
                <tbody>
                  {history.data?.days.slice(0, 60).map((d) => (
                    <tr key={d.id}>
                      <td>{d.work_date}</td><td>{d.display_name}</td><td>{hm(d.clock_in)}</td><td>{d.clock_out ? hm(d.clock_out) : '—'}</td>
                      <td>{d.duration_min != null ? durMin(d.duration_min) : '—'}</td>
                      <td className={d.arrival === 'late' || d.departure !== 'normal' ? 'late' : 'ok'}>
                        {d.arrival === 'late' ? t('st.late') : t('st.onTime')}{d.departure === 'early' ? ` · ${t('st.early')}` : d.departure === 'not_clocked' ? ` · ${t('st.notClocked')}` : ''}
                      </td>
                      <td><button className="ack" onClick={() => setFix(d)}>{t('team.correct')}</button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
      {fix && <CorrectDialog day={fix} onClose={() => setFix(null)} onDone={() => { setFix(null); showToast(t('team.corrected')); refresh(); }} />}
    </>
  );
}
