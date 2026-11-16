import { create } from 'zustand';
import * as api from '../api/client';
import type { Meeting, MeetingInvite } from '../api/client';
import { MeetingClient } from '../rtc/meetingMedia';
import { t } from '../i18n';
import { useApp } from './app';

export interface Room {
  id: string;
  title: string;
  kind: 'meeting' | 'video_call';
  hostId: number;
  status: 'connecting' | 'live';
  notice: string; // a short problem to show in the room (camera refused, ...)
  startedAt: number;
}

let client: MeetingClient | null = null;
let ringTimer = 0;

interface MeetingsState {
  active: Meeting[];
  recent: Meeting[];
  invite: MeetingInvite | null; // somebody invites me: the screen rings until I answer
  room: Room | null;
  version: number; // bumped on every change of the media client so React re-renders
  composing: boolean;
  setComposing: (b: boolean) => void;
  load: () => Promise<void>;
  start: (invitees: number[], title: string, kind?: 'meeting' | 'video_call') => Promise<void>;
  join: (id: string) => Promise<void>;
  accept: () => Promise<void>;
  decline: () => Promise<void>;
  leave: () => Promise<void>;
  end: () => Promise<void>;
  addPeople: (ids: number[]) => Promise<void>;
  media: () => MeetingClient | null;
  toggleMic: () => void;
  toggleCam: () => Promise<void>;
  toggleScreen: () => Promise<void>;
  onEvent: (type: string, d: { id: string; declined_by?: number } & Partial<MeetingInvite>) => void;
  reset: () => void;
}

export const useMeetings = create<MeetingsState>((set, get) => ({
  active: [], recent: [], invite: null, room: null, version: 0, composing: false,

  setComposing: (composing) => set({ composing }),
  media: () => client,

  async load() {
    try {
      const [a, r] = await Promise.all([api.meetings('active'), api.meetings('recent')]);
      set({ active: a.meetings, recent: r.meetings });
    } catch { /* keep the previous lists */ }
  },

  async start(invitees, title, kind = 'meeting') {
    const m = await api.createMeeting({ title: title.trim() || undefined, kind, invitees });
    await get().join(m.id);
    void get().load();
  },

  async join(id) {
    if (get().room) await get().leave();
    window.clearTimeout(ringTimer);
    set({ invite: null });
    const [info, ice] = await Promise.all([api.meetingToken(id), api.iceServers()]);
    set({ room: { id, title: info.title, kind: info.kind, hostId: info.host_id, status: 'connecting', notice: '', startedAt: Date.now() } });
    const c = new MeetingClient({
      meetingId: id, token: info.token, iceServers: ice.ice_servers, video: true,
      onChange: () => {
        const r = get().room;
        if (r && c.connected && r.status !== 'live') set({ room: { ...r, status: 'live' } });
        set((s) => ({ version: s.version + 1 }));
      },
      onEnded: () => { void get().leave(); },
      onError: (code) => {
        const r = get().room;
        if (!r) return;
        if (code === 'full') { useApp.getState().showToast(t('meet.full')); void get().leave(); return; }
        if (code === 'closed') { void get().leave(); return; }
        set({ room: { ...r, notice: t('meet.err.negotiation') } });
      },
    });
    client = c;
    try {
      await c.start();
    } catch {
      await get().leave();
      throw new Error('media');
    }
    const r = get().room;
    if (r && (!c.micAvailable || !c.camAvailable)) {
      set({ room: { ...r, notice: !c.micAvailable ? t('meet.noMic') : t('meet.noCam') } });
    }
  },

  async accept() {
    const inv = get().invite;
    if (inv) await get().join(inv.id);
  },

  async decline() {
    const inv = get().invite;
    window.clearTimeout(ringTimer);
    set({ invite: null });
    if (inv) { try { await api.meetingDecline(inv.id); } catch { /* already over */ } }
    void get().load();
  },

  async leave() {
    const r = get().room;
    client?.stop();
    client = null;
    set({ room: null });
    if (r) { try { await api.meetingLeave(r.id); } catch { /* the media service reports it as well */ } }
    void get().load();
  },

  async end() {
    const r = get().room;
    client?.stop();
    client = null;
    set({ room: null });
    if (r) { try { await api.meetingEnd(r.id); } catch { /* not the host */ } }
    void get().load();
  },

  async addPeople(ids) {
    const r = get().room;
    if (!r || ids.length === 0) return;
    try { await api.meetingInvite(r.id, ids); } catch { /* refused: the meeting ended or is full */ }
  },

  toggleMic() { client?.setMic(!client.mic); },
  async toggleCam() { await client?.setCam(!client.cam); },
  async toggleScreen() {
    if (!client) return;
    if (client.screen) await client.stopScreen(); else await client.startScreen();
  },

  onEvent(type, d) {
    void get().load();
    const s = get();
    if (type === 'meeting_invite') {
      if (s.room?.id === d.id) return;
      set({ invite: { id: d.id, title: d.title ?? '', kind: d.kind ?? 'meeting', host_id: d.host_id ?? 0, host: d.host ?? '' } });
      window.clearTimeout(ringTimer);
      ringTimer = window.setTimeout(() => { if (get().invite?.id === d.id) set({ invite: null }); }, 45_000);
    } else if (type === 'meeting_ended') {
      if (s.invite?.id === d.id) { window.clearTimeout(ringTimer); set({ invite: null }); }
      if (s.room?.id === d.id) {
        if (d.declined_by) useApp.getState().showToast(t('meet.declined'));
        void get().leave();
      }
    } else if (s.room?.id === d.id) {
      set((st) => ({ version: st.version + 1 }));
    }
  },

  reset: () => {
    client?.stop();
    client = null;
    window.clearTimeout(ringTimer);
    set({ active: [], recent: [], invite: null, room: null, composing: false });
  },
}));

/** Starts a one-to-one video call: the other person is rung, and the room opens for me right away. */
export async function startVideoCall(userId: number): Promise<void> {
  try { await useMeetings.getState().start([userId], '', 'video_call'); } catch { useApp.getState().showToast(t('meet.err.start')); }
}
