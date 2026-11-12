import { useState } from 'react';
import { useApp } from '../store/app';
import { t } from '../i18n';
import { Icon, type IconName } from '../components/Icon';
import { Avatar, presenceColor } from '../components/Avatar';
import { initials } from '../lib';
import { people, person, type Person } from '../data/mock';

const keys: [string, string][] = [['1', ''], ['2', 'ABC'], ['3', 'DEF'], ['4', 'GHI'], ['5', 'JKL'], ['6', 'MNO'], ['7', 'PQRS'], ['8', 'TUV'], ['9', 'WXYZ'], ['*', ''], ['0', '+'], ['#', '']];

function Device({ k, icon, title, sub }: { k: 'desk' | 'pc'; icon: IconName; title: string; sub: string }) {
  const { devs, toggleDev } = useApp();
  const on = devs[k];
  return (
    <div className="dev">
      <div className="ic"><Icon name={icon} /></div>
      <div className="t"><b>{title}</b><span>{sub}</span></div>
      <span className={'pill' + (on ? '' : ' off')}>{on ? 'En ligne' : 'Désactivé'}</span>
      <button className={'sw' + (on ? '' : ' off')} role="switch" aria-checked={on} aria-label={title} onClick={() => toggleDev(k)} />
    </div>
  );
}

export function Phone() {
  const { user, presence, toggleStatusMenu, startCall, incoming } = useApp();
  const [dial, setDial] = useState('');
  const match = people.find((p) => p.ext === dial);
  const ext = user?.extension ?? '';

  function call() {
    if (!dial) return;
    const p: Person = match ?? { id: 0, name: dial, ini: '#', color: 'var(--slate)', role: 'Appel externe via opérateur', ext: dial, dept: '', site: '', line: 'on' };
    setDial('');
    startCall(p);
  }

  return (
    <>
      <div className="mock" role="note">{t('mock.banner')}</div>
      <div className="top">
        <div className="tt"><b>Téléphone</b><span>Votre numéro interne : {ext} · {user?.display_name}</span></div>
        <button className="stchip" onClick={(e) => { e.stopPropagation(); toggleStatusMenu(true); }}>
          <i style={{ background: presenceColor[presence] }} />{t(`status.${presence}`)} ▾
        </button>
      </div>
      {presence === 'dnd' && <div style={{ background: 'var(--alert-bg)', color: 'var(--alert-dark)', padding: '10px 24px', fontSize: 13 }}><b>{t('status.dnd')}</b> : vos appels entrants sont renvoyés vers la messagerie vocale.</div>}
      {presence === 'away' && <div style={{ background: 'var(--gold-light)', color: 'var(--tag-text)', padding: '10px 24px', fontSize: 13 }}><b>{t('status.away')}</b> : vos appels sont renvoyés vers le poste de délégation.</div>}
      <div className="phone">
        <div className="card">
          <h4>Clavier</h4>
          <div className="disp" aria-live="polite">{dial || ' '}</div>
          <div className="who2">{match ? `${match.name} — ${match.role}` : dial.length >= 4 ? 'Numéro externe' : ''}</div>
          <div className="pad">
            {keys.map(([k, s]) => <button key={k} onClick={() => setDial((d) => (d + k).slice(0, 12))}>{k}<small>{s || ' '}</small></button>)}
          </div>
          <div className="callbtn">
            <button className="round" aria-label="Appeler" onClick={call}><Icon name="call" /></button>
            <button className="round" style={{ background: 'var(--bg)', color: 'var(--navy)' }} aria-label="Effacer" onClick={() => setDial((d) => d.slice(0, -1))}>⌫</button>
          </div>
        </div>
        <div>
          <div className="card">
            <h4>Mes appareils <span className="pill">Synchronisés</span></h4>
            <Device k="desk" icon="desk" title="Poste de bureau" sub="Téléphone IP (SIP) · rattaché à votre numéro" />
            <Device k="pc" icon="pc" title="Application PC" sub="EximPhone Desktop v0.1" />
            <div style={{ fontSize: 12, color: 'var(--muted)', lineHeight: 1.5 }}>Les appels vers le <b>{ext}</b> sonnent sur tous les appareils actifs. Décrochez sur l'un, les autres s'arrêtent.</div>
          </div>
          <div className="card hist" style={{ marginTop: 24 }}>
            <h4>Raccourcis</h4>
            {[8, 2, 5].map((id) => {
              const p = person(id);
              return (
                <button key={id} className="it" onClick={() => startCall(p)}>
                  <Avatar p={p} /><div className="t"><div className="n">{p.name}<small>{p.ext}</small></div><div className="s">{p.role}</div></div>
                  <span className="ib"><Icon name="call" /></span>
                </button>
              );
            })}
            <button className="btn gold" style={{ width: '100%', marginTop: 12 }} onClick={incoming}>Simuler un appel entrant</button>
          </div>
        </div>
      </div>
      <span className="sr-only">{initials(user?.display_name ?? '')}</span>
    </>
  );
}
