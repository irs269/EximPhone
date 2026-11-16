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
}

interface Msg {
  type: string;
  sdp?: string;
  you?: number;
  user?: number;
  peers?: { user: number; mic: boolean; cam: boolean; screen: boolean }[];
  mic?: boolean;
  cam?: boolean;
  screen?: boolean;
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
    if (!p) { p = { user, mic: true, cam: false, screen: false }; this.peers.set(user, p); }
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
        (m.peers ?? []).forEach((p) => { const v = this.peer(p.user); v.mic = p.mic; v.cam = p.cam; v.screen = p.screen; });
        await this.captured; // the user may still be answering the camera and microphone prompt
        if (this.closed) return;
        await this.setupConnections();
        this.connected = true;
        this.notify();
        break;
      case 'peer_joined': if (m.user) this.peer(m.user); this.notify(); break;
      case 'peer_left': if (m.user) this.peers.delete(m.user); this.notify(); break;
      case 'state':
        if (m.user) { const v = this.peer(m.user); v.mic = !!m.mic; v.cam = !!m.cam; v.screen = !!m.screen; if (!v.screen) delete v.screenStream; }
        this.notify();
        break;
      case 'publish_answer': this.answerWaiter?.(m.sdp ?? ''); this.answerWaiter = null; break;
      case 'sub_offer': this.queue = this.queue.then(() => this.answerSubscribe(m.sdp ?? '')).catch(() => undefined); break;
      case 'ended': this.closed = true; this.opt.onEnded(); break;
      case 'error': this.opt.onError(m.error?.includes('full') ? 'full' : 'negotiation'); break;
    }
  }

  private async setupConnections(): Promise<void> {
    const cfg: RTCConfiguration = { iceServers: this.opt.iceServers };
    this.sub = new RTCPeerConnection(cfg);
    this.sub.ontrack = (e) => this.onRemoteTrack(e);
    this.pub = new RTCPeerConnection(cfg);

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

  private sendState(): void { this.send({ type: 'state', mic: this.mic, cam: this.cam, screen: this.screen }); }

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
    track.onended = () => { void this.stopScreen(); }; // the browser's own "Stop sharing" button
    this.sendState();
    await this.renegotiatePublish();
    this.notify();
    return true;
  }

  async stopScreen(): Promise<void> {
    if (!this.screen) return;
    this.screen = false;
    this.localScreen?.getTracks().forEach((t) => t.stop());
    if (this.screenSender && this.pub) { try { this.pub.removeTrack(this.screenSender); } catch { /* already closed */ } }
    if (this.localScreen) delete this.sources[this.localScreen.id];
    this.localScreen = null;
    this.screenSender = null;
    this.sendState();
    await this.renegotiatePublish();
    this.notify();
  }

  /** Leaves the meeting (the server notices the closed socket) and releases the camera, microphone and screen. */
  stop(): void {
    this.closed = true;
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
