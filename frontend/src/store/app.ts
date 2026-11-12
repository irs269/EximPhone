import { create } from 'zustand';
import * as api from '../api/client';
import { ApiError, type Day, type LogoutSummary, type User } from '../api/client';
import { t } from '../i18n';
import { useChat } from './chat';
import {
  initialAnnouncements, initialCalls, person,
  type Announcement, type CallLog, type Person, type Presence,
} from '../data/mock';

export type View = 'chat' | 'phone' | 'dir' | 'att' | 'ann';
export type Dialog =
  | { kind: 'arrival'; resumed: boolean }
  | { kind: 'logout'; summary: LogoutSummary }
  | { kind: 'bye'; time: string; durationMin: number; name: string }
  | null;
export interface Call { who: Person; mode: 'out' | 'in' | 'live'; flags: Record<string, boolean> }

interface State {
  // --- session (real backend) ---
  phase: 'loading' | 'out' | 'in';
  user: User | null;
  today: Day | null;
  loginError: string;
  dialog: Dialog;
  bootstrap: () => Promise<void>;
  login: (username: string, password: string) => Promise<void>;
  askLogout: () => Promise<void>;
  confirmLogout: (reason: string) => Promise<string | null>;
  closeDialog: () => void;
  // --- UI + placeholder modules ---
  view: View;
  cur: string;
  query: string;
  presence: Presence;
  statusMenu: boolean;
  toast: string;
  devs: { desk: boolean; pc: boolean };
  calls: CallLog[];
  anns: Announcement[];
  call: Call | null;
  setView: (v: View) => void;
  setCur: (c: string) => void;
  setQuery: (q: string) => void;
  setPresence: (p: Presence) => void;
  toggleStatusMenu: (open?: boolean) => void;
  showToast: (m: string) => void;
  toggleDev: (k: 'desk' | 'pc') => void;
  ack: (id: number) => void;
  startCall: (p: Person) => void;
  incoming: () => void;
  answer: () => void;
  hang: (secs: number) => void;
  toggleFlag: (k: string) => void;
}

const nowHM = () => new Date().toTimeString().slice(0, 5);

function loginMessage(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.status === 401) return t('err.invalid');
    if (e.status === 429) return t('err.tooMany');
    if (e.status === 403) return e.message.includes('network') ? t('err.network') : e.message.includes('disabled') ? t('err.disabled') : e.message;
    if (e.status === 503) return t('err.directory');
  }
  return t('err.generic');
}

export const useApp = create<State>((set, get) => ({
  phase: 'loading', user: null, today: null, loginError: '', dialog: null,

  async bootstrap() {
    try {
      if (await api.refresh()) {
        const r = await api.me();
        set({ phase: 'in', user: r.user, today: r.attendance, presence: 'on' });
        useChat.getState().start(r.user.id);
        return;
      }
    } catch { /* fall through to login */ }
    set({ phase: 'out' });
  },

  async login(username, password) {
    set({ loginError: '' });
    try {
      const r = await api.login(username, password);
      set({ phase: 'in', user: r.user, today: r.attendance, presence: 'on', view: 'chat',
        dialog: { kind: 'arrival', resumed: !r.new_clock_in } });
      useChat.getState().start(r.user.id);
    } catch (e) {
      set({ loginError: loginMessage(e) });
    }
  },

  async askLogout() {
    try {
      set({ dialog: { kind: 'logout', summary: await api.logoutSummary() } });
    } catch {
      set({ phase: 'out', user: null, today: null });
    }
  },

  async confirmLogout(reason) {
    try {
      const name = get().user?.display_name.split(' ')[0] ?? '';
      const r = await api.logout(reason);
      useChat.getState().stop();
      set({ phase: 'out', user: null, today: null, call: null,
        dialog: { kind: 'bye', time: r.clock_out, durationMin: r.duration_min, name } });
      return null;
    } catch (e) {
      return e instanceof ApiError && e.status === 422 ? t('err.reason') : t('err.generic');
    }
  },
  closeDialog: () => set({ dialog: null }),

  view: 'chat', cur: '', query: '', presence: 'on', statusMenu: false, toast: '',
  devs: { desk: true, pc: true },
  calls: initialCalls(), anns: initialAnnouncements(), call: null,

  setView(v) {
    set({ view: v, query: '' });
  },
  setCur: (cur) => set({ cur }),
  setQuery: (query) => set({ query }),
  setPresence(p) {
    set({ presence: p, statusMenu: false });
    void api.putPresence(p);
    useChat.setState((c) => ({ presence: { ...c.presence, [c.me]: p } }));
    get().showToast(t('toast.status', { s: t(`status.${p}`) }));
  },
  toggleStatusMenu: (open) => set((s) => ({ statusMenu: open ?? !s.statusMenu })),
  showToast(m) {
    set({ toast: m });
    window.setTimeout(() => { if (get().toast === m) set({ toast: '' }); }, 3200);
  },
  toggleDev: (k) => set((s) => ({ devs: { ...s.devs, [k]: !s.devs[k] } })),

  ack: (id) => set((s) => ({ anns: s.anns.map((a) => (a.id === id ? { ...a, ack: true } : a)) })),

  startCall(p) {
    if (get().call) return;
    set({ call: { who: p, mode: 'out', flags: {} } });
    window.setTimeout(() => {
      const c = get().call;
      if (c && c.who === p && c.mode === 'out') set({ call: { ...c, mode: 'live' } });
    }, 2200);
  },
  incoming() {
    const { call, presence, showToast } = get();
    if (call) return;
    const p = person(1);
    if (presence === 'dnd') {
      set((s) => ({ calls: [{ person: p.id, kind: 'miss', when: nowHM() }, ...s.calls] }));
      showToast(`${t('status.dnd')} : ${p.name}`);
      return;
    }
    if (presence === 'away') { showToast(`${t('status.away')} : ${p.name}`); return; }
    set({ call: { who: p, mode: 'in', flags: {} } });
    if (presence === 'meet') showToast(t('status.meet.d'));
  },
  answer: () => set((s) => (s.call ? { call: { ...s.call, mode: 'live' } } : {})),
  hang(secs) {
    const c = get().call;
    if (!c) return;
    set((s) => ({ call: null, calls: [{ person: c.who.id, kind: secs ? 'out' : 'miss', when: nowHM() }, ...s.calls] }));
  },
  toggleFlag: (k) => set((s) => (s.call ? { call: { ...s.call, flags: { ...s.call.flags, [k]: !s.call.flags[k] } } } : {})),
}));
