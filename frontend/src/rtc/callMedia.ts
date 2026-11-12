import { iceServers, mediaOffer, mediaToken } from '../api/client';

/** Opens one WebRTC audio leg to the media service for a call and plays what the other party says. */
export class CallMedia {
  private pc: RTCPeerConnection | null = null;
  private stream: MediaStream | null = null;
  private ctx: AudioContext | null = null;
  private audio = new Audio();
  micAvailable = true;
  onState: (s: RTCPeerConnectionState) => void = () => undefined;

  /** Microphone, or (when none is available) a silent track so the call still connects listen-only. */
  private async localTrack(): Promise<MediaStreamTrack> {
    try {
      this.stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } });
      this.micAvailable = true;
      return this.stream.getAudioTracks()[0];
    } catch {
      this.micAvailable = false;
      this.ctx = new AudioContext();
      void this.ctx.resume(); // a suspended context produces no frames, so no RTP would flow
      const dest = this.ctx.createMediaStreamDestination();
      const silence = this.ctx.createOscillator(); // inaudible source keeps the encoder running
      const gain = this.ctx.createGain();
      gain.gain.value = 0;
      silence.connect(gain).connect(dest);
      silence.start();
      this.stream = dest.stream;
      return dest.stream.getAudioTracks()[0];
    }
  }

  async start(callId: string): Promise<void> {
    this.stop();
    const [track, ice, token] = await Promise.all([this.localTrack(), iceServers(), mediaToken(callId)]);
    const pc = new RTCPeerConnection({ iceServers: ice.ice_servers });
    this.pc = pc;
    pc.addTrack(track, this.stream!);
    pc.ontrack = (e) => {
      this.audio.srcObject = e.streams[0] ?? new MediaStream([e.track]);
      void this.audio.play().catch(() => undefined);
    };
    pc.onconnectionstatechange = () => this.onState(pc.connectionState);

    await pc.setLocalDescription(await pc.createOffer());
    await new Promise<void>((resolve) => {
      if (pc.iceGatheringState === 'complete') return resolve();
      const done = () => { if (pc.iceGatheringState === 'complete') { pc.removeEventListener('icegatheringstatechange', done); resolve(); } };
      pc.addEventListener('icegatheringstatechange', done);
      window.setTimeout(resolve, 2500); // do not wait forever for slow candidates
    });
    const answer = await mediaOffer(callId, token.token, pc.localDescription!.sdp);
    await pc.setRemoteDescription({ type: 'answer', sdp: answer });
  }

  /** Mute stops sending audio; hold also silences what we hear. */
  setMuted(muted: boolean): void { this.stream?.getAudioTracks().forEach((t) => { t.enabled = !muted; }); }
  setHeld(held: boolean): void { this.setMuted(held); this.audio.muted = held; }

  /** Bytes received so far, used to verify that audio really flows. */
  async receivedBytes(): Promise<number> {
    if (!this.pc) return 0;
    let n = 0;
    (await this.pc.getStats()).forEach((r) => { if (r.type === 'inbound-rtp' && r.kind === 'audio') n += r.bytesReceived ?? 0; });
    return n;
  }

  stop(): void {
    this.pc?.close();
    this.pc = null;
    this.stream?.getTracks().forEach((t) => t.stop());
    this.stream = null;
    void this.ctx?.close();
    this.ctx = null;
    this.audio.srcObject = null;
  }
}
