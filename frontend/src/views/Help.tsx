import { useApp } from '../store/app';
import { useChat } from '../store/chat';
import { useCalls } from '../store/calls';
import { t, useLocale } from '../i18n';
import { Icon } from '../components/Icon';
import { UserAvatar } from '../components/Avatar';

export const faqIds = [1, 2, 3, 4, 5, 6, 7] as const;

export function Help() {
  const { user, setView } = useApp();
  const { users, presence, startDirect } = useChat();
  const dial = useCalls((c) => c.dial);
  const locale = useLocale((s) => s.locale);
  // IT support = employees of the IT department (the directory is the source of truth).
  // Online agents first, at most six (a large IT department would otherwise bury the FAQ).
  const agents = Object.values(users)
    .filter((u) => u.department === 'IT' && u.id !== user?.id)
    .sort((x, y) => Number(!!presence[y.id]) - Number(!!presence[x.id]) || x.display_name.localeCompare(y.display_name))
    .slice(0, 6);

  return (
    <>
      <div className="top"><div className="tt"><b>{t('help.title')}</b><span>{t('help.sub')}</span></div></div>
      <div className="prof" style={{ alignItems: 'flex-start' }}>
        <div className="help">
          <section className="card agents" aria-labelledby="help-contact">
            <h4 id="help-contact">{t('help.contact')}</h4>
            <p style={{ color: 'var(--muted)', fontSize: 13, marginBottom: 12 }}>{t('help.contactD')}</p>
            {agents.map((u) => (
              <div key={u.id} className="it">
                <UserAvatar u={u} status={presence[u.id]} />
                <div className="t"><div className="n">{u.display_name}<small>{u.extension}</small></div><div className="s">{u.department}</div></div>
                <div className="acts">
                  {u.extension && presence[u.id] && (
                    <button className="btn" onClick={() => void dial(u.id)}><Icon name="call" /> {t('help.callIT')}</button>
                  )}
                  <button className="btn gold" onClick={async () => { await startDirect(u.id); setView('chat'); }}><Icon name="chat" /> {t('help.msgIT')}</button>
                </div>
              </div>
            ))}
            {!agents.length && <div style={{ color: 'var(--muted)', fontSize: 13 }}>{t('help.noIT')}</div>}
          </section>

          <section className="card faq" aria-labelledby="help-faq">
            <h4 id="help-faq">{t('help.faq')}</h4>
            {faqIds.map((n) => (
              <details key={n} id={'faq' + n}>
                <summary>{t(`faq.q${n}` as 'faq.q1')}</summary>
                <p>{t(`faq.a${n}` as 'faq.a1')}</p>
              </details>
            ))}
          </section>

          <section className="card" aria-labelledby="help-info">
            <h4 id="help-info">{t('help.info')}</h4>
            <div className="kv">
              <span>{t('help.infoUser')}</span><b>{user?.display_name} ({user?.username})</b>
              <span>{t('help.infoExt')}</span><b>{user?.extension}</b>
              <span>{t('help.infoLang')}</span><b>{locale.toUpperCase()}</b>
              <span>{t('help.infoBrowser')}</span><b style={{ overflowWrap: 'anywhere' }}>{navigator.userAgent}</b>
            </div>
          </section>
        </div>
      </div>
    </>
  );
}
