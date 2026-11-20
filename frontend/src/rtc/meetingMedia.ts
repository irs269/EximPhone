/**
 * Browser side of a meeting. Two peer connections to the media service (an SFU), driven over one WebSocket:
 *  - `pub` sends my microphone, camera and screen (I make the offers, also when I start or stop sharing);
 *  - `sub` receives everybody else (the server makes the offers whenever somebody arrives, leaves or shares).
 * ICE is non-trickle: candidates are inside the SDP, like the one-to-one calls.
 */
export interface PeerView {
  user: number;
  mic: boolean;
  cam: boolean;
  screen: boolean;
  /** the shared screen can be remote-controlled (desktop app, whole screen) */
  rc: boolean;
  /** microphone + camera of that person (the audio is played from here) */
  camStream?: MediaStream;
  screenStream?: MediaStream;
}

export interface MeetingOptions {
  meetingId: string;
  token: string;
  iceServers: RTCIceServer[];
  video: boolean; // start with the camera on
  onChange: () => void;
  onEnded: () => void;
  onError: (code: 'full' | 'closed' | 'negotiation') => void;
  nameOf: (user: number) => string;
  /** a short notice about remote control to show to the person (refused, ended, ...) */
  onRcNotice: (code: string, who: string) => void;
}

/** The desktop app (Electron) exposes this; a plain browser does not, and then nobody can control this computer. */
interface DesktopRC {
  available(): Promise<boolean>;
  begin(name: string): Promise<{ ok: boolean; reason?: string }>;
  input(ev: unknown): void;
  end(): void;
  sharedScreen(): Promise<{ controllable: boolean }>;
  shareStopped(): void;
  onEnded(cb: (reason: string) => void): void;
}
const desktop = (): DesktopRC | null => (window as unknown as { eximDesktop?: { remoteControl?: DesktopRC } }).eximDesktop?.remoteControl ?? null;

export interface RcInput { k: 'm' | 'd' | 'u' | 'w' | 'kd' | 'ku'; x?: number; y?: number; b?: number; dx?: number; dy?: number; code?: string; key?: string }

interface Msg {
  type: string;
  sdp?: string;
  you?: number;
  user?: number;
  peers?: { user: number; mic: boolean; cam: boolean; screen: boolean; rc?: boolean }[];
  mic?: boolean;
  cam?: boolean;
  screen?: boolean;
  rc?: boolean;
  owner?: number;
  reason?: string;
  ev?: RcInput;
  error?: string;
}

const waitIce = (pc: RTCPeerConnection, ms = 2500) =>
  new Promise<void>((resolve) => {
    if (pc.iceGatheringState === 'complete') return resolve();
    const done = () => { if (pc.iceGatheringState === 'complete') { pc.removeEventListener('icegatheringstatechange', done); resolve(); } };
    pc.addEventListener('icegatheringstatechange', done);
    window.setTimeout(resolve, ms); // do not wait forever for slow candidates
  });

export class MeetingClient {
  peers = new Map<number, PeerView>();
  me = 0;
  mic = true;
  cam = false;
  screen = false;
  micAvailable = true;
  camAvailable = true;
  localStream: MediaStream | null = null; // microphone + camera
  localScreen: MediaStream | null = null;
  connected = false;
  /** remote control: who I am controlling, who controls my screen, my pending request, and any session in the room */
  controlling: number | null = null;
  controlledBy: number | null = null;
  rcPending: number | null = null;
  rcSession: { owner: number; controller: number } | null = null;
  rcCapable = false;
  /** why my share cannot be controlled: 'browser' (not the desktop app), 'window' (not a whole screen), 'driver' (desktop app without input driver) */
  rcReason: 'browser' | 'window' | 'driver' | null = null;

  private ws: WebSocket | null = null;
  private pub: RTCPeerConnection | null = null;
  private sub: RTCPeerConnection | null = null;
  private camSender: RTCRtpSender | null = null;
  private screenSender: RTCRtpSender | null = null;
  private sources: Record<string, 'cam' | 'screen'> = {};
  private answerWaiter: ((sdp: string) => void) | null = null;
  private queue: Promise<unknown> = Promise.resolve();
  private closed = false;
  private captured: Promise<void> = Promise.resolve();

  constructor(private opt: MeetingOptions) {}

  private notify() { this.opt.onChange(); }
  private send(m: object) { if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m)); }
  private peer(user: number): PeerView {
    let p = this.peers.get(user);
    if (!p) { p = { user, mic: true, cam: false, screen: false, rc: false }; this.peers.set(user, p); }
    return p;
  }

  async start(): Promise<void> {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${proto}://${location.host}/media/meetings/${this.opt.meetingId}/ws`);
    this.ws = ws;
    ws.onopen = () => this.send({ type: 'join', token: this.opt.token });
    ws.onmessage = (e) => { void this.onMessage(JSON.parse(e.data) as Msg); };
    ws.onclose = () => { if (!this.closed) { this.connected = false; this.opt.onError('closed'); } };
    // local devices are requested while the connection is being set up; the connections are only built once they are known
    this.captured = this.captureLocal(this.opt.video);
    await this.captured;
  }

  // ---------- local media ----------

  private async captureLocal(withVideo: boolean): Promise<void> {
    const stream = new MediaStream();
    try {
      const a = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } });
      a.getAudioTracks().forEach((t) => stream.addTrack(t));
      this.micAvailable = true;
    } catch {
      this.micAvailable = false;
      this.mic = false;
    }
    if (withVideo) {
      try {
        const v = await navigator.mediaDevices.getUserMedia({ video: { width: { ideal: 1280 }, height: { ideal: 720 }, frameRate: { ideal: 24, max: 30 } } });
        v.getVideoTracks().forEach((t) => stream.addTrack(t));
        this.cam = true;
        this.camAvailable = true;
      } catch {
        this.cam = false;
        this.camAvailable = false;
      }
    }
    this.localStream = stream;
    this.notify();
  }

  // ---------- signalling ----------

  private async onMessage(m: Msg): Promise<void> {
    switch (m.type) {
      case 'joined':
        this.me = m.you ?? 0;
        (m.peers ?? []).forEach((p) => { const v = this.peer(p.user); v.mic = p.mic; v.cam = p.cam; v.screen = p.screen; v.rc = !!p.rc; });
        await this.captured; // the user may still be answering the camera and microphone prompt
        if (this.closed) return;
        await this.setupConnections();
        this.connected = true;
        this.notify();
        break;
      case 'peer_joined': if (m.user) this.peer(m.user); this.notify(); break;
      case 'peer_left': if (m.user) this.peers.delete(m.user); this.notify(); break;
      case 'state':
        if (m.user) { const v = this.peer(m.user); v.mic = !!m.mic; v.cam = !!m.cam; v.screen = !!m.screen; v.rc = !!m.rc; if (!v.screen) delete v.screenStream; }
        this.notify();
        break;
      case 'publish_answer': this.answerWaiter?.(m.sdp ?? ''); this.answerWaiter = null; break;
      case 'sub_offer': this.queue = this.queue.then(() => this.answerSubscribe(m.sdp ?? '')).catch(() => undefined); break;
      case 'rc_request': void this.onControlRequest(m.user ?? 0); break;
      case 'rc_started': this.onControlStarted(m.owner ?? 0, m.user ?? 0); break;
      case 'rc_denied': this.onControlDenied(m.user ?? 0, m.reason ?? ''); break;
      case 'rc_stopped': this.onControlStopped(m.owner ?? 0, m.user ?? 0, m.reason ?? ''); break;
      case 'rc_input': if (m.ev && this.controlledBy !== null) desktop()?.input(m.ev); break;
      case 'ended': this.closed = true; this.opt.onEnded(); break;
      case 'error': this.opt.onError(m.error?.includes('full') ? 'full' : 'negotiation'); break;
    }
  }

  private async setupConnections(): Promise<void> {
    const cfg: RTCConfiguration = { iceServers: this.opt.iceServers };
    this.sub = new RTCPeerConnection(cfg);
    this.sub.ontrack = (e) => this.onRemoteTrack(e);
    this.pub = new RTCPeerConnection(cfg);
    desktop()?.onEnded((reason) => { // the stop button, the shortcut or the expiry of the native session on this computer
      if (this.controlledBy !== null) this.send({ type: 'rc_stop' });
      this.onControlStopped(this.me, this.controlledBy ?? 0, reason);
    });

    const stream = this.localStream ?? new MediaStream();
    const audio = stream.getAudioTracks()[0];
    const video = stream.getVideoTracks()[0];
    // one audio and one video transceiver are always there (they stay even when the camera is off), so that a
    // later change of device or switching the camera on again needs no renegotiation
    this.pub.addTransceiver(audio ?? 'audio', { direction: 'sendonly', streams: [stream] });
    const vTr = this.pub.addTransceiver(video ?? 'video', { direction: 'sendonly', streams: [stream], sendEncodings: [{ maxBitrate: 900_000 }] });
    this.camSender = vTr.sender;
    this.sources[stream.id] = 'cam';
    if (audio) audio.enabled = this.mic;
    this.sendState();
    await this.renegotiatePublish();
  }

  private renegotiatePublish(): Promise<void> {
    this.queue = this.queue.then(async () => {
      const pub = this.pub;
      if (!pub || this.closed) return;
      await pub.setLocalDescription(await pub.createOffer());
      await waitIce(pub);
      const answer = new Promise<string>((resolve) => { this.answerWaiter = resolve; });
      this.send({ type: 'publish_offer', sdp: pub.localDescription!.sdp, sources: this.sources });
      const sdp = await Promise.race([answer, new Promise<string>((_, rej) => window.setTimeout(() => rej(new Error('timeout')), 10_000))]);
      await pub.setRemoteDescription({ type: 'answer', sdp });
    }).catch(() => this.opt.onError('negotiation'));
    return this.queue as Promise<void>;
  }

  private async answerSubscribe(sdp: string): Promise<void> {
    const sub = this.sub;
    if (!sub || this.closed) return;
    await sub.setRemoteDescription({ type: 'offer', sdp });
    await sub.setLocalDescription(await sub.createAnswer());
    await waitIce(sub);
    this.send({ type: 'sub_answer', sdp: sub.localDescription!.sdp });
  }

  /** The server names every remote stream "<user>:<cam|screen>". */
  private onRemoteTrack(e: RTCTrackEvent): void {
    const stream = e.streams[0];
    if (!stream) return;
    const [u, source] = stream.id.split(':');
    const user = Number(u);
    if (!user || user === this.me) return;
    const p = this.peer(user);
    if (source === 'screen') p.screenStream = stream; else p.camStream = stream;
    stream.onremovetrack = () => {
      if (stream.getTracks().length === 0) {
        const q = this.peers.get(user);
        if (q) { if (q.screenStream === stream) delete q.screenStream; if (q.camStream === stream) delete q.camStream; }
      }
      this.notify();
    };
    e.track.onended = () => this.notify();
    this.notify();
  }

  private sendState(): void { this.send({ type: 'state', mic: this.mic, cam: this.cam, screen: this.screen, rc: this.rcCapable }); }

  // ---------- controls ----------

  setMic(on: boolean): void {
    this.mic = on && this.micAvailable;
    this.localStream?.getAudioTracks().forEach((t) => { t.enabled = this.mic; });
    this.sendState();
    this.notify();
  }

  async setCam(on: boolean): Promise<void> {
    if (!this.camSender) return;
    if (!on) {
      this.localStream?.getVideoTracks().forEach((t) => { t.stop(); this.localStream?.removeTrack(t); });
      await this.camSender.replaceTrack(null);
      this.cam = false;
    } else {
      try {
        const v = await navigator.mediaDevices.getUserMedia({ video: { width: { ideal: 1280 }, height: { ideal: 720 }, frameRate: { ideal: 24, max: 30 } } });
        const track = v.getVideoTracks()[0];
        this.localStream?.addTrack(track);
        await this.camSender.replaceTrack(track);
        this.cam = true;
        this.camAvailable = true;
      } catch {
        this.cam = false;
        this.camAvailable = false;
      }
    }
    this.sendState();
    this.notify();
  }

  async startScreen(): Promise<boolean> {
    if (this.screen || !this.pub) return this.screen;
    let display: MediaStream;
    try {
      display = await navigator.mediaDevices.getDisplayMedia({ video: { frameRate: { ideal: 15, max: 30 } }, audio: false });
    } catch {
      return false; // the user cancelled the picker
    }
    const track = display.getVideoTracks()[0];
    track.contentHint = 'detail'; // text and slides: keep sharpness over smoothness
    this.localScreen = display;
    this.sources[display.id] = 'screen';
    const tr = this.pub.addTransceiver(track, { direction: 'sendonly', streams: [display], sendEncodings: [{ maxBitrate: 2_000_000 }] });
    this.screenSender = tr.sender;
    this.screen = true;
    // in the desktop app a whole screen (not a window) can be remote-controlled, if the person later accepts
    const d = desktop();
    this.rcCapable = false;
    this.rcReason = d ? null : 'browser';
    if (d) {
      try {
        const available = await d.available();
        const controllable = (await d.sharedScreen()).controllable;
        this.rcCapable = available && controllable;
        this.rcReason = !available ? 'driver' : !controllable ? 'window' : null;
      } catch { this.rcReason = 'driver'; }
    }
    track.onended = () => { void this.stopScreen(); }; // the browser's own "Stop sharing" button
    this.sendState();
    await this.renegotiatePublish();
    this.notify();
    return true;
  }

  async stopScreen(): Promise<void> {
    if (!this.screen) return;
    this.screen = false;
    this.rcCapable = false;
    desktop()?.shareStopped();
    this.localScreen?.getTracks().forEach((t) => t.stop());
    if (this.screenSender && this.pub) { try { this.pub.removeTrack(this.screenSender); } catch { /* already closed */ } }
    if (this.localScreen) delete this.sources[this.localScreen.id];
    this.localScreen = null;
    this.screenSender = null;
    this.sendState();
    await this.renegotiatePublish();
    this.notify();
  }

  // ---------- remote control ----------

  /** I ask to control the screen of `owner`. */
  requestControl(owner: number): void {
    if (this.controlling !== null || this.rcPending !== null) return;
    this.rcPending = owner;
    this.send({ type: 'rc_request', user: owner });
    this.notify();
  }

  /** Ends the session I am part of, as the controller or as the owner. */
  stopControl(): void {
    this.send({ type: 'rc_stop' });
    if (this.controlledBy !== null) desktop()?.end();
    this.rcPending = null;
    this.notify();
  }

  /** Sends one mouse or keyboard event to the person I control. */
  sendInput(ev: RcInput): void {
    if (this.controlling !== null) this.send({ type: 'rc_input', ev });
  }

  /** Somebody asks to control my screen: only the native dialog of the desktop app can accept. */
  private async onControlRequest(controller: number): Promise<void> {
    const d = desktop();
    let ok = false;
    if (d && this.rcCapable) {
      try { ok = (await d.begin(this.opt.nameOf(controller))).ok; } catch { ok = false; }
    }
    if (ok) {
      this.controlledBy = controller;
      this.send({ type: 'rc_accept', user: controller });
      // if the server does not confirm (the request had expired meanwhile), the native session must not stay open
      window.setTimeout(() => { if (this.controlledBy === controller && this.rcSession?.owner !== this.me) { desktop()?.end(); this.controlledBy = null; this.notify(); } }, 4000);
    } else {
      this.send({ type: 'rc_deny', user: controller });
    }
    this.notify();
  }

  /**
   * While my screen is being controlled, favour smoothness over sharpness: 30 images per second, a higher bitrate and
   * "motion" content (the encoder then keeps the frame rate up instead of the detail). Back to text-friendly settings afterwards.
   */
  private async tuneScreen(controlled: boolean): Promise<void> {
    const track = this.localScreen?.getVideoTracks()[0];
    if (!track || !this.screenSender) return;
    track.contentHint = controlled ? 'motion' : 'detail';
    try { await track.applyConstraints({ frameRate: { ideal: controlled ? 30 : 15, max: 30 } }); } catch { /* keep the current rate */ }
    try {
      const p = this.screenSender.getParameters();
      if (p.encodings?.length) {
        p.encodings[0].maxBitrate = controlled ? 4_000_000 : 2_000_000;
        p.encodings[0].maxFramerate = controlled ? 30 : 15;
        await this.screenSender.setParameters(p);
      }
    } catch { /* not supported: the defaults stay */ }
  }

  private onControlStarted(owner: number, controller: number): void {
    this.rcSession = { owner, controller };
    if (controller === this.me) { this.controlling = owner; this.rcPending = null; }
    if (owner === this.me) { this.controlledBy = controller; void this.tuneScreen(true); }
    this.notify();
  }

  private onControlDenied(owner: number, reason: string): void {
    if (this.rcPending === owner) this.rcPending = null;
    this.opt.onRcNotice('denied.' + reason, this.opt.nameOf(owner));
    this.notify();
  }

  private onControlStopped(owner: number, controller: number, reason: string): void {
    const mine = owner === this.me || controller === this.me;
    this.rcSession = null;
    if (this.controlling === owner) this.controlling = null;
    if (this.controlledBy !== null && owner === this.me) { desktop()?.end(); this.controlledBy = null; if (this.screen) void this.tuneScreen(false); }
    if (this.rcPending === owner) this.rcPending = null;
    if (mine && reason) this.opt.onRcNotice('stopped.' + reason, this.opt.nameOf(owner === this.me ? controller : owner));
    this.notify();
  }

  /** Leaves the meeting (the server notices the closed socket) and releases the camera, microphone and screen. */
  stop(): void {
    this.closed = true;
    if (this.controlledBy !== null) desktop()?.end();
    this.send({ type: 'leave' });
    this.ws?.close();
    this.pub?.close();
    this.sub?.close();
    this.localStream?.getTracks().forEach((t) => t.stop());
    this.localScreen?.getTracks().forEach((t) => t.stop());
    this.peers.clear();
    this.connected = false;
  }
}
