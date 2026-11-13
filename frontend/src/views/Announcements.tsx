import { useEffect, useState } from 'react';
import { announcementAcks, orgMeta, publishAnnouncement, type AckPerson, type AnnouncementItem, type OrgMeta } from '../api/client';
import { useApp } from '../store/app';
import { useAnn } from '../store/ann';
import { t, dateLocale } from '../i18n';

const when = (iso: string) => new Date(iso).toLocaleString(dateLocale(), { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

function Followup({ a, onClose }: { a: AnnouncementItem; onClose: () => void }) {
  const [data, setData] = useState<{ total: number; read: AckPerson[]; unread: AckPerson[] } | null>(null);
  useEffect(() => { void announcementAcks(a.id).then(setData).catch(() => setData(null)); }, [a.id]);
  return (
    <div id="dlg" role="presentation">
      <div className="dbox" role="dialog" aria-modal="true" aria-label={t('ann.followup')} style={{ width: 520 }}>
        <div className="dh"><b>{t('ann.followup')}</b><span>{a.title}</span></div>
        <div className="form" style={{ maxHeight: 360, overflow: 'auto' }}>
          {!data ? '…' : (
            <>
              <p><b>{data.read.length}</b> / {data.total} {t('ann.read').toLowerCase()}</p>
              <h4 style={{ margin: '12px 0 6px' }}>{t('ann.unread')} ({data.unread.length})</h4>
              {data.unread.map((p) => <div key={p.id} className="kv"><span>{p.display_name}</span>{p.department} · {p.site}</div>)}
              <h4 style={{ margin: '12px 0 6px' }}>{t('ann.read')} ({data.read.length})</h4>
              {data.read.map((p) => <div key={p.id} className="kv"><span>{p.display_name}</span>{p.acked_at ? when(p.acked_at) : ''}</div>)}
            </>
          )}
        </div>
        <div className="bt"><button className="btn" onClick={onClose}>{t('bye.close')}</button></div>
      </div>
    </div>
  );
}

function Compose({ onDone }: { onDone: () => void }) {
  const showToast = useApp((s) => s.showToast);
  const [meta, setMeta] = useState<OrgMeta | null>(null);
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [level, setLevel] = useState('normal');
  const [target, setTarget] = useState('all:0');
  const [busy, setBusy] = useState(false);
  useEffect(() => { void orgMeta().then(setMeta).catch(() => undefined); }, []);

  async function submit() {
    const [kind, id] = target.split(':');
    setBusy(true);
    try {
      const r = await publishAnnouncement({ title, body, level, target: { kind, id: Number(id) || undefined } });
      showToast(t('ann.published', { n: r.recipients }));
      onDone();
    } catch { showToast(t('err.generic')); }
    finally { setBusy(false); }
  }

  return (
    <div className="card" style={{ marginBottom: 8 }}>
      <h4>{t('ann.new')}</h4>
      <label htmlFor="at">{t('ann.title')}</label>
      <input id="at" className="inp" maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} />
      <label htmlFor="ab">{t('ann.body')}</label>
      <textarea id="ab" className="inp" maxLength={5000} value={body} onChange={(e) => setBody(e.target.value)} />
      <div style={{ display: 'flex', gap: 12, marginTop: 4 }}>
        <div style={{ flex: 1 }}>
          <label htmlFor="al">{t('ann.level')}</label>
          <select id="al" className="inp" value={level} onChange={(e) => setLevel(e.target.value)}>
            <option value="normal">{t('ann.normal')}</option><option value="urgent">{t('ann.urgent')}</option>
          </select>
        </div>
        <div style={{ flex: 1 }}>
          <label htmlFor="ag">{t('ann.target')}</label>
          <select id="ag" className="inp" value={target} onChange={(e) => setTarget(e.target.value)}>
            <option value="all:0">{t('ann.everyone')}</option>
            {meta?.departments.map((d) => <option key={'d' + d.id} value={`department:${d.id}`}>{t('ann.dept')} {d.name}</option>)}
            {meta?.sites.map((s) => <option key={'s' + s.id} value={`site:${s.id}`}>{t('ann.site')} {s.name}</option>)}
          </select>
        </div>
      </div>
      <button className="btn" style={{ marginTop: 14 }} disabled={busy || !title.trim() || !body.trim()} onClick={submit}>{t('ann.publish')}</button>
    </div>
  );
}

export function Announcements() {
  const { items, ack, load } = useAnn();
  const roles = useApp((s) => s.roles);
  const canPublish = roles.includes('communication') || roles.includes('direction');
  const canFollow = canPublish || roles.includes('hr');
  const [composing, setComposing] = useState(false);
  const [follow, setFollow] = useState<AnnouncementItem | null>(null);
  useEffect(() => { void load(); }, [load]);

  return (
    <>
      <div className="top">
        <div className="tt"><b>{t('nav.announcements')}</b><span>{t('ann.sub')}</span></div>
        {canPublish && <button className="btn" onClick={() => setComposing((c) => !c)}>{t('ann.new')}</button>}
      </div>
      <div className="feed">
        {composing && <Compose onDone={() => { setComposing(false); void load(); }} />}
        {!items.length && <div className="empty">{t('list.none')}</div>}
        {items.map((a) => (
          <article key={a.id} id={'a' + a.id} className={'ann' + (a.level === 'urgent' ? ' urg' : '')}>
            <div className="h">
              <span className={'tag' + (a.level === 'urgent' ? ' r' : '')}>{a.level === 'urgent' ? t('ann.urgent') : t('ann.normal')}</span>
              <small style={{ color: 'var(--muted)' }}>{when(a.created_at)} · {a.target_kind === 'all' ? t('ann.everyone') : a.target_name}</small>
            </div>
            <h5>{a.title}</h5>
            <p style={{ marginTop: 6, whiteSpace: 'pre-wrap' }}>{a.body}</p>
            <div className="f">
              <span>{t('ann.by')} <b>{a.author}</b></span>
              <span style={{ display: 'flex', gap: 8 }}>
                {canFollow && <button className="ack" onClick={() => setFollow(a)}>{t('ann.followup')}</button>}
                <button className={'ack' + (a.acked_at ? ' done' : '')} disabled={!!a.acked_at} onClick={() => void ack(a.id)}>
                  {a.acked_at ? t('ann.acked') : t('ann.ack')}
                </button>
              </span>
            </div>
          </article>
        ))}
      </div>
      {follow && <Followup a={follow} onClose={() => setFollow(null)} />}
    </>
  );
}
