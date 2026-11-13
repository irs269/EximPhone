import { useEffect, useState } from 'react';
import { useApp } from '../store/app';
import { useCalls } from '../store/calls';
import { useChat } from '../store/chat';
import { Icon, type IconName } from './Icon';
import { UserAvatar } from './Avatar';
import { mmss } from '../lib';
import { t } from '../i18n';

function TransferPicker() {
  const { call, transfer, setTransferOpen } = useCalls();
  const { users, presence, me } = useChat();
  const [q, setQ] = useState('');
  const list = Object.values(users)
    .filter((u) => u.id !== me && u.id !== call?.peer.id && presence[u.id] && presence[u.id] !== 'dnd' && u.display_name.toLowerCase().includes(q.toLowerCase()))
    .sort((a, b) => a.display_name.localeCompare(b.display_name));
  return (
    <div className="xfer" role="dialog" aria-label={t('call.transferTitle')}>
      <input className="inp" autoFocus placeholder={t('call.transferTo')} aria-label={t('call.transferTo')} value={q} onChange={(e) => setQ(e.target.value)} />
      <div className="picker">
        {list.map((u) => (
          <button key={u.id} className="it" onClick={() => void transfer(u.id)}>
            <UserAvatar u={u} status={presence[u.id]} />
            <div className="t"><div className="n">{u.display_name}<small>{u.extension}</small></div><div className="s">{u.department}</div></div>
          </button>
        ))}
        {!list.length && <div className="sec" style={{ color: 'var(--muted)' }}>{t('call.noneAvail')}</div>}
      </div>
      <button className="btn ghost" style={{ width: '100%' }} onClick={() => setTransferOpen(false)}>{t('out.cancel')}</button>
    </div>
  );
}

export function CallOverlay() {
  const { call, transferOpen, answer, hangup, toggleMute, toggleHold, setTransferOpen } = useCalls();
  const devs = useApp((s) => s.devs);
  const [secs, setSecs] = useState(0);
  const start = call?.mode === 'live' ? call.answeredAt : undefined;

  useEffect(() => {
    if (!start) { setSecs(0); return; }
    const tick = () => setSecs(Math.max(0, Math.round((Date.now() - start) / 1000)));
    tick();
    const id = window.setInterval(tick, 1000);
    return () => window.clearInterval(id);
  }, [start]);

  if (!call) return null;
  const { peer, mode, direction } = call;
  const label = mode === 'transferring' ? t('call.transferring', { name: peer.display_name })
    : mode === 'ringing' ? (direction === 'in' ? (call.transferredBy ? t('call.transferredBy', { name: call.transferredBy.display_name }) : t('call.incoming')) : t('call.outgoing'))
    : call.held ? t('call.held') : call.remoteHeld ? t('call.remoteHeld') : t('call.live');
  const peerUser = { id: peer.id, display_name: peer.display_name };

  const controls: { k: 'mute' | 'hold' | 'transfer'; icon: IconName; text: string; on: boolean; run: () => void }[] = [
    { k: 'mute', icon: 'mic', text: t('call.mute'), on: call.muted, run: toggleMute },
    { k: 'hold', icon: 'pause', text: t('call.hold'), on: call.held, run: () => void toggleHold() },
    { k: 'transfer', icon: 'transfer', text: t('call.transfer'), on: transferOpen, run: () => setTransferOpen(!transferOpen) },
  ];

  return (
    <div id="call" role="presentation">
      <div className="cbox" role="dialog" aria-modal="true" aria-label={label}>
        <div className={mode !== 'live' ? 'ring' : ''}>
          <div className="lbl">{label}</div>
          <UserAvatar u={peerUser} />
          <b>{peer.display_name}</b>
          <div className="sub">{t('call.ext', { ext: peer.extension ?? '' })}</div>
          {mode === 'ringing' && direction === 'in' && (
            <>
              <div className="ring-on">
                {devs.desk && <span><Icon name="desk" /> {t('call.deskPhone')}</span>}
                {devs.pc && <span><Icon name="pc" /> {t('call.pc')}</span>}
                {call.silent && <span>{t('call.silentRing')}</span>}
              </div>
              <div className="callbtn">
                <button className="round red" aria-label={t('call.decline')} onClick={() => void hangup()}><Icon name="call" rotate={135} /></button>
                <button className="round" aria-label={t('call.answer')} onClick={() => void answer()}><Icon name="call" /></button>
              </div>
            </>
          )}
          {((mode === 'ringing' && direction === 'out') || mode === 'transferring') && (
            <>
              <div className="sub" style={{ marginTop: 14 }}>{t('call.ringing')}</div>
              <div className="callbtn"><button className="round red" aria-label={t('call.hangup')} onClick={() => void hangup()}><Icon name="call" rotate={135} /></button></div>
            </>
          )}
          {mode === 'live' && (
            <>
              <div className="tm" aria-live="off">{mmss(secs)}</div>
              {call.mediaState && call.mediaState !== 'connected' && <div className="sub">{t('call.audioConnecting', { s: call.mediaState })}</div>}
              {call.micMissing && <div className="sub">{t('call.micMissing')}</div>}
              <div className="ctrl">
                {controls.map((c) => (
                  <button key={c.k} className={c.on ? 'act' : ''} aria-pressed={c.on} onClick={c.run}><i><Icon name={c.icon} /></i>{c.text}</button>
                ))}
              </div>
              {transferOpen && <TransferPicker />}
              <button className="round red" aria-label={t('call.hangup')} onClick={() => void hangup()}><Icon name="call" rotate={135} /></button>
            </>
          )}
          <div className="enc"><Icon name="lock" size={11} /> {t('call.relay')}</div>
        </div>
      </div>
    </div>
  );
}
