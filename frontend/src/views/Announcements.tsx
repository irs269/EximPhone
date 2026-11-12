import { useApp } from '../store/app';
import { t } from '../i18n';

export function Announcements() {
  const { anns, ack, showToast } = useApp();
  return (
    <>
      <div className="mock" role="note">{t('mock.banner')}</div>
      <div className="top"><div className="tt"><b>Annonces officielles</b><span>Notes de service et circulaires de la direction</span></div></div>
      <div className="feed">
        {anns.map((a) => (
          <article key={a.id} id={'a' + a.id} className={'ann' + (a.urgent ? ' urg' : '')}>
            <div className="h"><span className={'tag' + (a.urgent ? ' r' : '')}>{a.tag}</span><small style={{ color: 'var(--muted)' }}>{a.date}</small></div>
            <h5>{a.title}</h5>
            <p style={{ marginTop: 6 }}>{a.body}</p>
            <div className="f">
              <span>Publié par <b>{a.by}</b></span>
              <button className={'ack' + (a.ack ? ' done' : '')} onClick={() => { ack(a.id); showToast('Accusé de réception enregistré'); }}>
                {a.ack ? '✓ Lu et compris' : 'Accuser réception'}
              </button>
            </div>
          </article>
        ))}
      </div>
    </>
  );
}
