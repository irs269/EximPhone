import { create } from 'zustand';
import * as api from '../api/client';
import { ApiError, type Day, type LogoutSummary, type User } from '../api/client';
import { t } from '../i18n';
import { useChat } from './chat';
import { useCalls } from './calls';
import { useAnn } from './ann';
import { useTickets } from './tickets';
import type { Presence } from '../data/mock';

export type View = 'chat' | 'phone' | 'dir' | 'att' | 'ann' | 'tickets' | 'team' | 'audit' | 'help';
export type Dialog =
  | { kind: 'arrival'; resumed: boolean }
  | { kind: 'logout'; summary: LogoutSummary }
  | { kind: 'bye'; time: string; durationMin: number; name: string }
  | null;

interface State {
  // --- session (real backend) ---
  phase: 'loading' | 'out' | 'in';
  user: User | null;
  roles: string[];
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
  setView: (v: View) => void;
  setCur: (c: string) => void;
  setQuery: (q: string) => void;
  setPresence: (p: Presence) => void;
  toggleStatusMenu: (open?: boolean) => void;
  showToast: (m: string) => void;
  toggleDev: (k: 'desk' | 'pc') => void;
}


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
  phase: 'loading', user: null, roles: ['employee'], today: null, loginError: '', dialog: null,

  async bootstrap() {
    try {
      if (await api.refresh()) {
        const r = await api.me();
        set({ phase: 'in', user: r.user, roles: r.roles, today: r.attendance, presence: 'on' });
        useChat.getState().start(r.user.id);
        void useAnn.getState().load();
        void useTickets.getState().loadSummary();
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
      void api.me().then((m) => set({ roles: m.roles })).catch(() => undefined);
      void useAnn.getState().load();
        void useTickets.getState().loadSummary();
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
      useAnn.getState().reset();
      useTickets.getState().reset();
      useCalls.getState().reset();
      set({ phase: 'out', user: null, roles: ['employee'], today: null,
        dialog: { kind: 'bye', time: r.clock_out, durationMin: r.duration_min, name } });
      return null;
    } catch (e) {
      return e instanceof ApiError && e.status === 422 ? t('err.reason') : t('err.generic');
    }
  },
  closeDialog: () => set({ dialog: null }),

  view: 'chat', cur: '', query: '', presence: 'on', statusMenu: false, toast: '',
  devs: { desk: true, pc: true },
  

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


}));
