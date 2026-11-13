import { create } from 'zustand';
import * as api from '../api/client';
import type { Ticket, TicketSummary, TicketView } from '../api/client';
import { t } from '../i18n';
import { useApp } from './app';

const empty: TicketSummary = { to_decide: 0, to_assign: 0, to_do: 0, mine_open: 0, waiting: 0, is_head: false };

interface TicketEventData { id: number; kind: string; title: string; actor_id: number }
type ToastKey = 'created' | 'approved' | 'rejected' | 'assigned' | 'resolved' | 'cancelled' | 'comment' | 'started';
const toastKinds: ToastKey[] = ['created', 'approved', 'rejected', 'assigned', 'resolved', 'cancelled', 'comment', 'started'];

interface TicketsState {
  summary: TicketSummary;
  view: TicketView;
  list: Ticket[];
  selected: Ticket | null;
  composing: boolean;
  loadSummary: () => Promise<void>;
  loadList: () => Promise<void>;
  setView: (v: TicketView) => void;
  select: (id: number | null) => Promise<void>;
  setComposing: (b: boolean) => void;
  /** Replaces a ticket after an action (the API returns the updated ticket) and refreshes the rest. */
  applied: (tk: Ticket) => Promise<void>;
  onEvent: (d: TicketEventData, me: number | undefined) => void;
  reset: () => void;
}

export const useTickets = create<TicketsState>((set, get) => ({
  summary: empty, view: '', list: [], selected: null, composing: false,

  async loadSummary() {
    try { set({ summary: await api.ticketSummary() }); } catch { /* keep the previous counters */ }
  },
  async loadList() {
    try { set({ list: (await api.tickets(get().view)).tickets }); } catch { /* keep the previous list */ }
  },
  setView(v) { set({ view: v }); void get().loadList(); },
  async select(id) {
    if (id === null) { set({ selected: null }); return; }
    try { set({ selected: await api.ticket(id) }); } catch { set({ selected: null }); }
  },
  setComposing: (composing) => set({ composing }),
  async applied(tk) {
    await Promise.all([get().select(tk.id), get().loadList(), get().loadSummary()]);
  },
  onEvent(d, me) {
    void get().loadSummary();
    void get().loadList();
    if (get().selected?.id === d.id) void get().select(d.id);
    if (d.actor_id !== me && toastKinds.includes(d.kind as ToastKey)) {
      useApp.getState().showToast(t(`tk.toast.${d.kind as ToastKey}`, { title: d.title }));
    }
  },
  reset: () => set({ summary: empty, view: '', list: [], selected: null, composing: false }),
}));
