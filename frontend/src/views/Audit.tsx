import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { auditLog } from '../api/client';
import { t } from '../i18n';

const PAGE = 50;

export function Audit() {
  const [action, setAction] = useState('');
  const [actor, setActor] = useState('');
  const [page, setPage] = useState(0);
  const q = useQuery({
    queryKey: ['audit', action, actor, page],
    queryFn: () => auditLog({ action, actor, limit: String(PAGE), offset: String(page * PAGE) }),
  });
  const entries = q.data?.entries ?? [];

  return (
    <>
      <div className="top"><div className="tt"><b>{t('nav.audit')}</b><span>{t('audit.sub')}</span></div></div>
      <div className="att">
        <div className="card">
          <h4>
            <span style={{ display: 'flex', gap: 10 }}>
              <input className="inp" style={{ width: 220 }} aria-label={t('audit.action')} placeholder={t('audit.action')} value={action} onChange={(e) => { setAction(e.target.value); setPage(0); }} />
              <input className="inp" style={{ width: 180 }} aria-label={t('audit.actor')} placeholder={t('audit.actor')} value={actor} onChange={(e) => { setActor(e.target.value); setPage(0); }} />
            </span>
          </h4>
          <table>
            <thead><tr><th>{t('att.date')}</th><th>{t('audit.actor')}</th><th>{t('audit.action')}</th><th>IP</th><th>{t('audit.details')}</th></tr></thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.id}>
                  <td style={{ whiteSpace: 'nowrap' }}>{new Date(e.at).toLocaleString('fr-FR')}</td><td>{e.actor || '—'}</td><td><b>{e.action}</b></td><td>{e.ip}</td>
                  <td style={{ fontSize: 12, color: 'var(--muted)', maxWidth: 360, wordBreak: 'break-word' }}>{JSON.stringify(e.details).slice(0, 160)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
            <button className="btn ghost" disabled={page === 0} onClick={() => setPage((p) => p - 1)}>{t('audit.newer')}</button>
            <button className="btn ghost" disabled={entries.length < PAGE} onClick={() => setPage((p) => p + 1)}>{t('audit.older')}</button>
          </div>
        </div>
      </div>
    </>
  );
}
