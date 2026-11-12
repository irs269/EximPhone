import { useEffect, useState } from 'react';
import { useApp } from '../store/app';
import { Avatar } from './Avatar';
import { Icon, type IconName } from './Icon';
import { mmss } from '../lib';

const controls: [string, IconName, string][] = [['mute', 'mic', 'Muet'], ['hold', 'pause', 'Attente'], ['transfer', 'transfer', 'Transférer'], ['pad', 'grid', 'Clavier']];

export function CallOverlay() {
  const { call, devs, user, hang, answer, toggleFlag, showToast } = useApp();
  const [secs, setSecs] = useState(0);
  const live = call?.mode === 'live';

  useEffect(() => {
    if (!live) { setSecs(0); return; }
    const id = window.setInterval(() => setSecs((s) => s + 1), 1000);
    return () => window.clearInterval(id);
  }, [live]);

  if (!call) return null;
  const { who, mode, flags } = call;
  const ext = user?.extension ?? '';
  const label = mode === 'in' ? `Appel entrant · Poste ${ext}` : mode === 'out' ? 'Appel en cours…' : flags.hold ? 'En attente' : 'En communication';

  return (
    <div id="call" role="presentation">
      <div className="cbox" role="dialog" aria-modal="true" aria-label={label}>
        <div className={mode !== 'live' ? 'ring' : ''}>
          <div className="lbl">{label}</div>
          <Avatar p={who} dot={false} />
          <b>{who.name}</b>
          <div className="sub">{who.role} · Poste {who.ext}</div>
          {mode === 'in' && (
            <>
              <div className="ring-on">
                {devs.desk && <span><Icon name="desk" /> Poste de bureau</span>}
                {devs.pc && <span><Icon name="pc" /> PC</span>}
                {!devs.desk && !devs.pc && <span>Aucun appareil actif</span>}
              </div>
              <div className="callbtn">
                <button className="round red" aria-label="Refuser" onClick={() => hang(0)}><Icon name="call" rotate={135} /></button>
                <button className="round" aria-label="Décrocher" onClick={answer}><Icon name="call" /></button>
              </div>
            </>
          )}
          {mode === 'out' && (
            <>
              <div className="sub" style={{ marginTop: 14 }}>Sonnerie…</div>
              <div className="callbtn"><button className="round red" aria-label="Raccrocher" onClick={() => hang(0)}><Icon name="call" rotate={135} /></button></div>
            </>
          )}
          {mode === 'live' && (
            <>
              <div className="tm" aria-live="off">{mmss(secs)}</div>
              <div className="ctrl">
                {controls.map(([k, icon, text]) => (
                  <button key={k} className={flags[k] ? 'act' : ''} aria-pressed={!!flags[k]}
                    onClick={() => { toggleFlag(k); if (k === 'transfer') showToast('Transfert : composez le poste de destination'); }}>
                    <i><Icon name={icon} /></i>{text}
                  </button>
                ))}
              </div>
              <button className="round red" aria-label="Raccrocher" onClick={() => hang(secs)}><Icon name="call" rotate={135} /></button>
            </>
          )}
          <div className="enc"><Icon name="lock" size={11} /> Appel chiffré (SRTP) · Réseau interne Exim Bank</div>
        </div>
      </div>
    </div>
  );
}
