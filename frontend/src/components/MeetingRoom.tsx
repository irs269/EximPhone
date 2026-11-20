import { useEffect, useRef, useState } from 'react';
import type { RcInput } from '../rtc/meetingMedia';
import { t } from '../i18n';
import { useChat } from '../store/chat';
import { useMeetings } from '../store/meetings';
import { Icon } from './Icon';
import { UserAvatar } from './Avatar';

const mmss = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;

/** One participant (or one shared screen). The audio of a participant plays from the same stream as their video. */
type Control = { mode: 'can' | 'pending' | 'active'; onAsk: () => void; onInput: (ev: RcInput) => void };

/**
 * Sits on top of a shared screen while I control it: turns my mouse and keyboard into events relative to the shared
 * picture (0..1), so that they land at the right place whatever the size of the tile or of the remote screen.
 */
function ControlSurface({ video, onInput }: { video: HTMLVideoElement | null; onInput: (ev: RcInput) => void }) {
  const box = useRef<HTMLDivElement>(null);
  const last = useRef(0);
  useEffect(() => { box.current?.focus(); }, []);
  useEffect(() => { // wheel events must be cancelable to stop the page from scrolling: a native, non-passive listener
    const el = box.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => { e.preventDefault(); onInput({ k: 'w', dx: e.deltaX, dy: e.deltaY }); };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, [onInput]);

  /** position inside the picture itself (the video is letterboxed when its shape differs from the tile) */
  const pos = (e: React.MouseEvent) => {
    const r = (video ?? box.current)?.getBoundingClientRect();
    if (!r) return { x: 0, y: 0 };
    const vw = video?.videoWidth || r.width, vh = video?.videoHeight || r.height;
    const scale = Math.min(r.width / vw, r.height / vh);
    const cw = vw * scale, ch = vh * scale;
    const x = (e.clientX - r.left - (r.width - cw) / 2) / cw, y = (e.clientY - r.top - (r.height - ch) / 2) / ch;
    return { x: Math.min(1, Math.max(0, x)), y: Math.min(1, Math.max(0, y)) };
  };
  const key = (down: boolean) => (e: React.KeyboardEvent) => {
    e.preventDefault(); e.stopPropagation();
    onInput({ k: down ? 'kd' : 'ku', code: e.code, key: e.key });
  };
  return (
    <div ref={box} className="mcapture" tabIndex={0} role="application" aria-label="remote control"
      onMouseMove={(e) => { const n = performance.now(); if (n - last.current > 16) { last.current = n; onInput({ k: 'm', ...pos(e) }); } }}
      onMouseDown={(e) => { e.preventDefault(); box.current?.focus(); onInput({ k: 'd', b: e.button === 2 ? 2 : e.button === 1 ? 1 : 0, ...pos(e) }); }}
      onMouseUp={(e) => { e.preventDefault(); onInput({ k: 'u', b: e.button === 2 ? 2 : e.button === 1 ? 1 : 0, ...pos(e) }); }}
      onContextMenu={(e) => e.preventDefault()}
      onKeyDown={key(true)} onKeyUp={key(false)} />
  );
}

function Tile({ name, userId, stream, showVideo, micOn, self, screen, fit, ctl }: {
  name: string; userId: number; stream?: MediaStream | null; showVideo: boolean; micOn: boolean; self?: boolean; screen?: boolean; fit?: 'cover' | 'contain'; ctl?: Control;
}) {
  const ref = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    const v = ref.current;
    if (v && v.srcObject !== (stream ?? null)) {
      v.srcObject = stream ?? null;
      if (stream) void v.play().catch(() => undefined);
    }
  }, [stream]);
  return (
    <div className={'mtile' + (screen ? ' screen' : '') + (ctl?.mode === 'active' ? ' controlled' : '')} role="group" aria-label={name}>
      {/* the video element stays mounted even when the camera is off, so that the person is still heard */}
      <video ref={ref} autoPlay playsInline muted={!!self} style={{ objectFit: fit ?? 'cover', visibility: showVideo ? 'visible' : 'hidden' }} />
      {!showVideo && <div className="mavatar"><UserAvatar u={{ id: userId, display_name: name }} /></div>}
      {ctl?.mode === 'active' && <ControlSurface video={ref.current} onInput={ctl.onInput} />}
      {ctl && ctl.mode !== 'active' && (
        <button type="button" className="mrcbtn" disabled={ctl.mode === 'pending'} onClick={ctl.onAsk}>
          {ctl.mode === 'pending' ? t('meet.rc.waiting') : t('meet.rc.ask')}
        </button>
      )}
      <div className="mname">
        {!micOn && !screen && <span className="mmute" title={t('meet.muted')}><Icon name="mic" size={13} /></span>}
        {screen && <Icon name="screen" size={13} />}
        <span>{screen ? t('meet.screenOf', { name }) : self ? `${name} (${t('mail.me')})` : name}</span>
      </div>
    </div>
  );
}

export function MeetingRoom() {
  const room = useMeetings((s) => s.room);
  useMeetings((s) => s.version); // re-render when the media client changes
  const { media, toggleMic, toggleCam, toggleScreen, leave, end, addPeople } = useMeetings();
  const sendInput = useRef<(ev: RcInput) => void>(() => undefined);
  const users = useChat((s) => s.users);
  const me = useChat((s) => s.me);
  const [secs, setSecs] = useState(0);
  const [panel, setPanel] = useState<'' | 'people' | 'invite'>('');
  const [q, setQ] = useState('');

  useEffect(() => {
    if (!room) return;
    const tick = () => setSecs(Math.max(0, Math.round((Date.now() - room.startedAt) / 1000)));
    tick();
    const id = window.setInterval(tick, 1000);
    return () => window.clearInterval(id);
  }, [room?.id, room?.startedAt]); // eslint-disable-line react-hooks/exhaustive-deps

  const c = media();
  if (!room || !c) return null;

  const nameOf = (id: number) => users[id]?.display_name ?? '#' + id;
  sendInput.current = (ev) => c.sendInput(ev);
  const onInput = (ev: RcInput) => sendInput.current(ev);
  const controlOf = (owner: number, self: boolean): Control | undefined => {
    if (self) return undefined;
    if (c.controlling === owner) return { mode: 'active', onAsk: () => undefined, onInput };
    if (c.rcPending === owner) return { mode: 'pending', onAsk: () => undefined, onInput };
    const p = c.peers.get(owner);
    if (p?.rc && c.controlling === null && c.rcPending === null && !c.rcSession) return { mode: 'can', onAsk: () => c.requestControl(owner), onInput };
    return undefined;
  };
  const peers = [...c.peers.values()];
  const screens = [
    ...(c.screen && c.localScreen ? [{ id: me, name: nameOf(me), stream: c.localScreen as MediaStream, self: true }] : []),
    ...peers.filter((p) => p.screenStream).map((p) => ({ id: p.user, name: nameOf(p.user), stream: p.screenStream as MediaStream, self: false })),
  ];
  const tiles = [
    <Tile key="me" name={nameOf(me)} userId={me} stream={c.localStream} showVideo={c.cam && !!c.localStream?.getVideoTracks().length} micOn={c.mic} self />,
    ...peers.map((p) => (
      <Tile key={p.user} name={nameOf(p.user)} userId={p.user} stream={p.camStream} showVideo={p.cam && !!p.camStream?.getVideoTracks().length} micOn={p.mic} />
    )),
  ];
  const count = tiles.length;
  const cols = count <= 1 ? 1 : count <= 4 ? 2 : count <= 9 ? 3 : 4;
  const isHost = room.hostId === me;
  const canShare = typeof navigator.mediaDevices?.getDisplayMedia === 'function';
  const inRoom = new Set([me, ...peers.map((p) => p.user)]);
  const candidates = Object.values(users)
    .filter((u) => !inRoom.has(u.id) && (u.display_name + ' ' + u.department).toLowerCase().includes(q.toLowerCase()))
    .sort((a, b) => a.display_name.localeCompare(b.display_name)).slice(0, 30);

  return (
    <div id="meeting" role="dialog" aria-modal="true" aria-label={room.title}>
      <header className="mhead">
        <div><b>{room.title}</b><span>{t('meet.people', { n: count })} · {mmss(secs)}</span></div>
        {room.status === 'connecting' && <span className="mstate">{t('meet.connecting')}</span>}
      </header>
      {room.notice && <div className="mnotice" role="status">{room.notice}</div>}
      {c.controlling !== null && (
        <div className="mrcbar" role="status"><span>{t('meet.rc.controlling', { name: nameOf(c.controlling) })} · {t('meet.rc.hint', { name: nameOf(c.controlling) })}</span>
          <button type="button" onClick={() => c.stopControl()}>{t('meet.rc.stop')}</button></div>
      )}
      {c.controlledBy !== null && (
        <div className="mrcbar danger" role="alert"><span>{t('meet.rc.controlledBy', { name: nameOf(c.controlledBy) })}</span>
          <button type="button" onClick={() => c.stopControl()}>{t('meet.rc.stop')}</button></div>
      )}
      {c.rcSession && c.controlling === null && c.controlledBy === null && (
        <div className="mrcbar quiet" role="status"><span>{t('meet.rc.other', { controller: nameOf(c.rcSession.controller), owner: nameOf(c.rcSession.owner) })}</span></div>
      )}

      <div className={'mstage' + (screens.length ? ' withscreen' : '')}>
        {screens.length > 0 && (
          <div className="mscreens">
            {screens.map((s) => <Tile key={'s' + s.id} name={s.name} userId={s.id} stream={s.stream} showVideo micOn screen fit="contain" self={s.self} ctl={controlOf(s.id, s.self)} />)}
          </div>
        )}
        <div className="mgrid" style={screens.length ? undefined : { gridTemplateColumns: `repeat(${cols}, 1fr)` }}>{tiles}</div>
        {panel === 'people' && (
          <aside className="mpanel" aria-label={t('meet.participants')}>
            <h4>{t('meet.participants')}</h4>
            <ul>
              <li><b>{nameOf(me)}</b> ({t('mail.me')}){!c.mic && ' · ' + t('meet.muted')}</li>
              {peers.map((p) => <li key={p.user}><b>{nameOf(p.user)}</b>{!p.mic && ' · ' + t('meet.muted')}{p.screen && ' · ' + t('meet.sharing')}</li>)}
            </ul>
          </aside>
        )}
        {panel === 'invite' && (
          <aside className="mpanel" aria-label={t('meet.invite')}>
            <h4>{t('meet.invite')}</h4>
            <input className="inp" autoFocus placeholder={t('list.search')} aria-label={t('list.search')} value={q} onChange={(e) => setQ(e.target.value)} />
            <ul className="mpick">
              {candidates.map((u) => (
                <li key={u.id}><button type="button" onClick={() => { void addPeople([u.id]); }}><b>{u.display_name}</b><small>{u.department}</small><span>+</span></button></li>
              ))}
            </ul>
          </aside>
        )}
      </div>

      <footer className="mbar">
        <button className={'mctl' + (c.mic ? '' : ' off')} aria-pressed={!c.mic} onClick={() => toggleMic()} disabled={!c.micAvailable}
          title={c.mic ? t('meet.mute') : t('meet.unmute')}><Icon name="mic" /><span>{c.mic ? t('meet.mute') : t('meet.unmute')}</span></button>
        <button className={'mctl' + (c.cam ? '' : ' off')} aria-pressed={!c.cam} onClick={() => void toggleCam()}
          title={c.cam ? t('meet.camOff') : t('meet.camOn')}><Icon name="video" /><span>{c.cam ? t('meet.camOff') : t('meet.camOn')}</span></button>
        {canShare && (
          <button className={'mctl' + (c.screen ? ' on' : '')} aria-pressed={c.screen} onClick={() => void toggleScreen()}
            title={c.screen ? t('meet.stopShare') : t('meet.share')}><Icon name="screen" /><span>{c.screen ? t('meet.stopShare') : t('meet.share')}</span></button>
        )}
        <button className={'mctl' + (panel === 'people' ? ' on' : '')} onClick={() => setPanel(panel === 'people' ? '' : 'people')}><Icon name="dir" /><span>{t('meet.participants')}</span></button>
        {room.kind === 'meeting' && (
          <button className={'mctl' + (panel === 'invite' ? ' on' : '')} onClick={() => { setQ(''); setPanel(panel === 'invite' ? '' : 'invite'); }}><Icon name="chat" /><span>{t('meet.invite')}</span></button>
        )}
        <button className="mctl leave" onClick={() => void leave()}><Icon name="call" rotate={135} /><span>{t('meet.leave')}</span></button>
        {isHost && room.kind === 'meeting' && <button className="mctl leave alt" onClick={() => void end()}><span>{t('meet.endAll')}</span></button>}
      </footer>
    </div>
  );
}

/** Somebody invites me (a meeting, or a one-to-one video call): the screen rings until I answer. */
export function MeetingInvite() {
  const inv = useMeetings((s) => s.invite);
  const { accept, decline } = useMeetings();
  const users = useChat((s) => s.users);
  if (!inv) return null;
  const host = users[inv.host_id] ?? { id: inv.host_id, display_name: inv.host };
  return (
    <div id="call" role="presentation">
      <div className="cbox" role="alertdialog" aria-modal="true" aria-label={inv.kind === 'video_call' ? t('meet.incomingCall') : t('meet.invitation')}>
        <div className="ring">
          <div className="lbl">{inv.kind === 'video_call' ? t('meet.incomingCall') : t('meet.invitation')}</div>
          <UserAvatar u={host} />
          <b>{inv.host}</b>
          {inv.kind === 'meeting' && <div className="sub">{inv.title}</div>}
          <div className="callbtn">
            <button className="round red" aria-label={t('call.decline')} onClick={() => void decline()}><Icon name="call" rotate={135} /></button>
            <button className="round" aria-label={t('meet.join')} autoFocus onClick={() => void accept()}><Icon name="video" /></button>
          </div>
        </div>
      </div>
    </div>
  );
}
