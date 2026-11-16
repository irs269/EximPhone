import { useEffect, useRef, useState } from 'react';
import { t } from '../i18n';
import { useChat } from '../store/chat';
import { useMeetings } from '../store/meetings';
import { Icon } from './Icon';
import { UserAvatar } from './Avatar';

const mmss = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;

/** One participant (or one shared screen). The audio of a participant plays from the same stream as their video. */
function Tile({ name, userId, stream, showVideo, micOn, self, screen, fit }: {
  name: string; userId: number; stream?: MediaStream | null; showVideo: boolean; micOn: boolean; self?: boolean; screen?: boolean; fit?: 'cover' | 'contain';
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
    <div className={'mtile' + (screen ? ' screen' : '')} role="group" aria-label={name}>
      {/* the video element stays mounted even when the camera is off, so that the person is still heard */}
      <video ref={ref} autoPlay playsInline muted={!!self} style={{ objectFit: fit ?? 'cover', visibility: showVideo ? 'visible' : 'hidden' }} />
      {!showVideo && <div className="mavatar"><UserAvatar u={{ id: userId, display_name: name }} /></div>}
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

      <div className={'mstage' + (screens.length ? ' withscreen' : '')}>
        {screens.length > 0 && (
          <div className="mscreens">
            {screens.map((s) => <Tile key={'s' + s.id} name={s.name} userId={s.id} stream={s.stream} showVideo micOn screen fit="contain" self={s.self} />)}
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
