import { create } from 'zustand';
import * as api from '../api/client';
import type { CallLogEntry, Party } from '../api/client';
import { CallMedia } from '../rtc/callMedia';
import { t } from '../i18n';
import { useApp } from './app';

export interface ActiveCall {
  id: string;
  peer: Party;
  direction: 'in' | 'out';
  mode: 'ringing' | 'live' | 'transferring';
  silent: boolean;       // "in meeting": ring without sound
  muted: boolean;
  held: boolean;         // we put the call on hold
  remoteHeld: boolean;   // the other party did
  transferredBy?: Party;
  answeredAt?: number;
  mediaState?: string;
  micMissing?: boolean;
}

interface CallsState {
  call: ActiveCall | null;
  log: CallLogEntry[];
  transferOpen: boolean;
  dial: (userId: number) => Promise<void>;
  answer: () => Promise<void>;
  hangup: () => Promise<void>;
  toggleMute: () => void;
  toggleHold: () => Promise<void>;
  transfer: (userId: number) => Promise<void>;
  setTransferOpen: (open: boolean) => void;
  loadLog: () => Promise<void>;
  onEvent: (type: string, data: any) => void;
  reset: () => void;
}

const media = new CallMedia();
export const callMedia = media; // exposed for diagnostics (stats) and tests

const toast = (m: string) => useApp.getState().showToast(m);
const outcomeMessage: Record<string, string> = {
  busy: 'Occupé : cette personne est déjà en communication.',
  dnd: 'Ne pas déranger : appel renvoyé vers la messagerie vocale.',
  offline: "Cette personne n'a pas encore pointé son arrivée (hors ligne).",
  rejected: 'Appel refusé.', missed: 'Pas de réponse.',
};

export const useCalls = create<CallsState>((set, get) => {
  const patch = (p: Partial<ActiveCall>) => set((s) => (s.call ? { call: { ...s.call, ...p } } : s));

  async function connectMedia(id: string) {
    try {
      media.onState = (st) => patch({ mediaState: st });
      await media.start(id);
      patch({ micMissing: !media.micAvailable });
      if (!media.micAvailable) toast('Microphone indisponible : vous entendez votre correspondant sans pouvoir parler.');
    } catch {
      toast("Impossible d'établir l'audio de l'appel.");
    }
  }

  return {
    call: null, log: [], transferOpen: false,

    async dial(userId) {
      if (get().call) return;
      try {
        const r = await api.createCall(userId);
        if (r.state === 'ended') { toast(outcomeMessage[r.outcome ?? ''] ?? t('err.generic')); void get().loadLog(); return; }
        set({ call: { id: r.id, peer: r.to!, direction: 'out', mode: 'ringing', silent: false, muted: false, held: false, remoteHeld: false } });
      } catch { toast(t('err.generic')); }
    },

    async answer() {
      const c = get().call;
      if (!c || c.mode !== 'ringing' || c.direction !== 'in') return;
      try { await api.answerCall(c.id); } catch { set({ call: null }); }
    },

    async hangup() {
      const c = get().call;
      if (!c) return;
      try { await api.hangupCall(c.id); } catch { /* already ended: the event will clean up */ }
    },

    toggleMute() {
      const c = get().call;
      if (!c) return;
      media.setMuted(!c.muted || c.held);
      patch({ muted: !c.muted });
    },

    async toggleHold() {
      const c = get().call;
      if (!c || c.mode !== 'live') return;
      const held = !c.held;
      media.setHeld(held);
      if (!held) media.setMuted(c.muted);
      patch({ held });
      await api.holdCall(c.id, held).catch(() => undefined);
    },

    async transfer(userId) {
      const c = get().call;
      if (!c) return;
      try { await api.transferCall(c.id, userId); set({ transferOpen: false }); }
      catch { toast('Transfert impossible : le destinataire est indisponible.'); }
    },

    setTransferOpen: (transferOpen) => set({ transferOpen }),

    async loadLog() {
      try { set({ log: (await api.callLog()).calls }); } catch { /* keep the previous list */ }
    },

    reset() { media.stop(); set({ call: null, log: [], transferOpen: false }); },

    onEvent(type, data) {
      const c = get().call;
      switch (type) {
        case 'call_incoming':
          if (c) return; // already busy: the server answers "busy" to the caller
          set({ call: { id: data.call_id, peer: data.from, direction: 'in', mode: 'ringing', silent: !!data.silent, muted: false, held: false,
            remoteHeld: false, transferredBy: data.transferred_by } });
          break;
        case 'call_answered':
          if (!c || c.id !== data.call_id) break;
          if (data.via === 'sip' && c.direction === 'in') {
            // another device of ours (the desk phone) took the call: stop ringing here
            set({ call: null, transferOpen: false });
            toast('Appel pris sur votre poste de bureau.');
            break;
          }
          patch({ mode: 'live', answeredAt: Date.now() });
          void connectMedia(c.id);
          break;
        case 'call_hold':
          if (c && c.id === data.call_id) patch({ remoteHeld: !!data.on });
          break;
        case 'call_transferred': // we were handed over to someone else
          if (c && c.id === data.old_call_id) {
            media.stop();
            set({ call: { ...c, id: data.call_id, peer: data.to, mode: 'transferring', held: false, muted: false, remoteHeld: false, answeredAt: undefined } });
          }
          break;
        case 'call_ended':
          if (c && c.id === data.call_id) {
            media.stop();
            const secs = c.answeredAt ? Math.round((Date.now() - c.answeredAt) / 1000) : 0;
            if (data.outcome === 'answered') toast(`Appel terminé · ${Math.floor(secs / 60)} min ${secs % 60} s`);
            else if (data.outcome !== 'cancelled' && data.outcome !== 'transferred') toast(outcomeMessage[data.outcome] ?? t('err.generic'));
            set({ call: null, transferOpen: false });
          }
          void get().loadLog();
          break;
      }
    },
  };
});
