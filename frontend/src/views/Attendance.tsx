import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { history, type Day } from '../api/client';
import { useApp } from '../store/app';
import { t, dateLocale } from '../i18n';
import { durMin, hm } from '../lib';

export function dayStatus(d: Day): { cls: 'ok' | 'late' | 'ko'; label: string } {
  if (d.departure === 'not_clocked') return { cls: 'ko', label: t('st.notClocked') };
  if (d.arrival === 'late') return { cls: 'late', label: t('st.late') };
  if (d.departure === 'early') return { cls: 'late', label: t('st.early') };
  return { cls: 'ok', label: t('st.onTime') };
}

const dateLabel = (iso: string) =>
  new Date(iso + 'T00:00:00').toLocaleDateString(dateLocale(), { weekday: 'short', day: 'numeric', month: 'short' });

export function Attendance() {
  const { today, askLogout } = useApp();
  const { data } = useQuery({ queryKey: ['history'], queryFn: history });
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);

  const mins = today ? Math.max(0, Math.floor((now - new Date(today.clock_in).getTime()) / 60000)) : 0;
  const late = today?.arrival === 'late';
  const month = new Date().toISOString().slice(0, 7);
  const monthDays = (data?.days ?? []).filter((d) => d.work_date.startsWith(month)).length;
  const past = (data?.days ?? []).filter((d) => d.id !== today?.id);

  return (
    <>
      <div className="top">
        <div className="tt"><b>{t('att.title')}</b><span>{t('att.sub')}</span></div>
        <button className="btn red" onClick={askLogout}>{t('out.confirm')}</button>
      </div>
      <div className="att">
        <div className="kp">
          <div className="card"><small>{t('att.arrivalToday')}</small><b>{today ? hm(today.clock_in) : '—'}</b></div>
          <div className="card"><small>{t('att.presence')}</small><b>{durMin(mins)}</b></div>
          <div className="card"><small>{t('att.punctuality')}</small><b className={late ? 'late' : 'ok'}>{late ? t('st.late') : t('st.onTime')}</b></div>
          <div className="card"><small>{t('att.thisMonth')}</small><b>{t('att.days', { n: monthDays })}</b></div>
        </div>
        <div className="card">
          <h4>{t('att.history')}</h4>
          <table>
            <thead><tr><th>{t('att.date')}</th><th>{t('att.in')}</th><th>{t('att.out')}</th><th>{t('att.duration')}</th><th>{t('att.status')}</th></tr></thead>
            <tbody>
              {today && (
                <tr><td><b>{t('att.today')}</b></td><td>{hm(today.clock_in)}</td><td>—</td><td>{t('att.running')}</td>
                  <td className={late ? 'late' : 'ok'}>{late ? t('st.late') : t('st.present')}</td></tr>
              )}
              {past.map((d) => {
                const s = dayStatus(d);
                return (
                  <tr key={d.id}><td>{dateLabel(d.work_date)}</td><td>{hm(d.clock_in)}</td><td>{d.clock_out ? hm(d.clock_out) : '—'}</td>
                    <td>{d.duration_min != null ? durMin(d.duration_min) : '—'}</td><td className={s.cls}>{s.label}</td></tr>
                );
              })}
            </tbody>
          </table>
          <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 14, lineHeight: 1.5 }}>{t('att.footnote')}</div>
        </div>
      </div>
    </>
  );
}
