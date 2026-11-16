import { t, dateLocale } from '../i18n';
import type { Meeting } from '../api/client';
import { useApp } from '../store/app';
import { useMeetings } from '../store/meetings';

const when = (iso: string) => new Date(iso).toLocaleString(dateLocale(), { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

function Card({ m, live }: { m: Meeting; live: boolean }) {
  const { join } = useMeetings();
  const showToast = useApp((s) => s.showToast);
  const here = m.participants.filter((p) => p.status === 'joined');
  return (
    <article className="card mcard">
      <div className="mcard-t">
        <b>{m.kind === 'video_call' ? t('meet.videoCall') : m.title}</b>
        <small>{t('meet.host', { name: m.host })} · {when(m.created_at)}</small>
        <small>{live ? t('meet.joined', { n: here.length }) + (here.length ? ' : ' + here.map((p) => p.name).join(', ') : '') : t('meet.ended')}</small>
      </div>
      {live && <button className="btn" onClick={() => { void join(m.id).catch(() => showToast(t('meet.err.start'))); }}>{t('meet.join')}</button>}
    </article>
  );
}

export function Meetings() {
  const { active, recent, setComposing } = useMeetings();
  return (
    <>
      <div className="top">
        <div className="tt"><b>{t('nav.meetings')}</b><span>{t('meet.hint')}</span></div>
        <button className="btn gold" onClick={() => setComposing(true)}>{t('meet.new')}</button>
      </div>
      <div className="prof" style={{ alignItems: 'flex-start' }}>
        <div className="help">
          <section aria-labelledby="mt-active">
            <h4 id="mt-active" style={{ color: 'var(--navy)', marginBottom: 10 }}>{t('meet.active')}</h4>
            {active.length === 0 && <p style={{ color: 'var(--muted)' }}>{t('meet.empty')}</p>}
            <div style={{ display: 'grid', gap: 10 }}>{active.map((m) => <Card key={m.id} m={m} live />)}</div>
          </section>
          {recent.length > 0 && (
            <section aria-labelledby="mt-recent">
              <h4 id="mt-recent" style={{ color: 'var(--navy)', marginBottom: 10 }}>{t('meet.recent')}</h4>
              <div style={{ display: 'grid', gap: 10 }}>{recent.map((m) => <Card key={m.id} m={m} live={false} />)}</div>
            </section>
          )}
        </div>
      </div>
    </>
  );
}
