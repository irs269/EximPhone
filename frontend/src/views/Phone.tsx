import { useState } from 'react';
import { useApp } from '../store/app';
import { useCalls } from '../store/calls';
import { useChat } from '../store/chat';
import { t } from '../i18n';
import { Icon, type IconName } from '../components/Icon';
import { UserAvatar, presenceColor } from '../components/Avatar';

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
  const { user, presence, toggleStatusMenu, showToast } = useApp();
  const { dial: place } = useCalls();
  const { users, presence: lines } = useChat();
  const [dial, setDial] = useState('');
  const all = Object.values(users);
  const match = all.find((u) => u.extension === dial);
  const ext = user?.extension ?? '';
  const shortcuts = all.filter((u) => u.id !== user?.id && lines[u.id]).slice(0, 3);

  function call() {
    if (!dial) return;
    if (!match) { showToast(dial.startsWith('0') ? 'Appels externes : trunk opérateur non activé.' : 'Numéro interne inconnu.'); return; }
    if (match.id === user?.id) { showToast('Vous ne pouvez pas vous appeler.'); return; }
    setDial('');
    void place(match.id);
  }

  return (
    <>
      <div className="top">
        <div className="tt"><b>Téléphone</b><span>Votre numéro interne : {ext} · {user?.display_name}</span></div>
        <button className="stchip" onClick={(e) => { e.stopPropagation(); toggleStatusMenu(true); }}>
          <i style={{ background: presenceColor[presence] }} />{t(`status.${presence}`)} ▾
        </button>
      </div>
      {presence === 'dnd' && <div style={{ background: 'var(--alert-bg)', color: 'var(--alert-dark)', padding: '10px 24px', fontSize: 13 }}><b>{t('status.dnd')}</b> : vos appels entrants sont rejetés.</div>}
      {presence === 'meet' && <div style={{ background: 'var(--gold-light)', color: 'var(--tag-text)', padding: '10px 24px', fontSize: 13 }}><b>{t('status.meet')}</b> : les appels sonnent sans bruit.</div>}
      <div className="phone">
        <div className="card">
          <h4>Clavier</h4>
          <div className="disp" aria-live="polite">{dial || ' '}</div>
          <div className="who2">{match ? `${match.display_name} — ${match.department}` : dial.length >= 4 ? (dial.startsWith('0') ? 'Numéro externe' : 'Numéro inconnu') : ''}</div>
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
            <Device k="desk" icon="desk" title="Poste de bureau" sub="Téléphone IP (SIP) · sonne en même temps que le PC" />
            <Device k="pc" icon="pc" title="Application PC" sub="EximPhone Desktop v0.1 · audio WebRTC" />
            <div style={{ fontSize: 12, color: 'var(--muted)', lineHeight: 1.5 }}>Les appels vers le <b>{ext}</b> sonnent sur tous les appareils actifs. Décrochez sur l'un, les autres s'arrêtent.</div>
          </div>
          <div className="card hist" style={{ marginTop: 24 }}>
            <h4>Collègues en ligne</h4>
            {shortcuts.map((u) => (
              <button key={u.id} className="it" onClick={() => void place(u.id)}>
                <UserAvatar u={u} status={lines[u.id]} /><div className="t"><div className="n">{u.display_name}<small>{u.extension}</small></div><div className="s">{u.department}</div></div>
                <span className="ib"><Icon name="call" /></span>
              </button>
            ))}
            {!shortcuts.length && <div style={{ color: 'var(--muted)', fontSize: 13 }}>Personne d'autre n'est connecté pour le moment.</div>}
          </div>
        </div>
      </div>
    </>
  );
}
