import { create } from 'zustand';
import * as api from '../api/client';
import type { Conversation, DirUser, Message } from '../api/client';
import { useCalls } from './calls';
import { useAnn } from './ann';
import { connectSocket, disconnectSocket, sendSocket, type ServerEvent } from '../api/socket';

interface ChatState {
  users: Record<number, DirUser>;
  presence: Record<number, string>;
  convs: Conversation[];
  msgs: Record<number, Message[]>;
  hasMore: Record<number, boolean>;
  typing: Record<number, number>; // conversation -> user id currently typing
  active: number | null;
  newChatOpen: boolean;
  me: number;

  start: (meId: number) => void;
  stop: () => void;
  openConv: (id: number) => Promise<void>;
  loadOlder: (id: number) => Promise<void>;
  send: (id: number, text: string, attachmentId?: string) => Promise<void>;
  remove: (id: number, messageId: number) => Promise<void>;
  startDirect: (userId: number) => Promise<void>;
  createGroup: (name: string, ids: number[]) => Promise<void>;
  notifyTyping: (id: number) => void;
  setNewChat: (open: boolean) => void;
}

const empty = { users: {}, presence: {}, convs: [], msgs: {}, hasMore: {}, typing: {}, active: null, newChatOpen: false, me: 0 };
let lastTypingSent = 0;
const typingTimers: Record<number, number> = {};

const byId = (list: DirUser[]) => Object.fromEntries(list.map((u) => [u.id, u]));
const merge = (old: Message[] = [], incoming: Message[]) => {
  const map = new Map(old.map((m) => [m.id, m]));
  for (const m of incoming) map.set(m.id, m);
  return [...map.values()].sort((a, b) => a.id - b.id);
};

export const useChat = create<ChatState>((set, get) => {
  async function loadConvs() {
    const r = await api.conversations();
    set({ convs: r.conversations });
  }

  async function resync() {
    const [u, p] = await Promise.all([api.users(), api.getPresence()]);
    set({ users: byId(u.users), presence: Object.fromEntries(Object.entries(p.presence).map(([k, v]) => [Number(k), v])) });
    await loadConvs();
    const a = get().active;
    if (a) { const r = await api.messages(a); set((s) => ({ msgs: { ...s.msgs, [a]: merge(s.msgs[a], r.messages) } })); }
  }

  async function refreshConvMessages(id: number) {
    const r = await api.messages(id);
    set((s) => ({ msgs: { ...s.msgs, [id]: merge(s.msgs[id], r.messages) } }));
  }

  function onEvent({ type, data }: ServerEvent) {
    const s = get();
    switch (type) {
      case 'presence':
        set({ presence: { ...s.presence, [data.user_id]: data.status } });
        break;
      case 'message': {
        const m = data as Message;
        const mine = m.sender_id === s.me;
        const visible = s.active === m.conversation_id && document.visibilityState === 'visible';
        set((st) => ({
          msgs: st.msgs[m.conversation_id] ? { ...st.msgs, [m.conversation_id]: merge(st.msgs[m.conversation_id], [m]) } : st.msgs,
          convs: st.convs.some((c) => c.id === m.conversation_id)
            ? st.convs.map((c) => (c.id === m.conversation_id ? { ...c, last_message: m, unread: mine || visible ? c.unread : c.unread + 1 } : c))
              .sort((a, b) => (b.last_message?.id ?? 0) - (a.last_message?.id ?? 0))
            : st.convs,
        }));
        if (!s.convs.some((c) => c.id === m.conversation_id)) void loadConvs();
        if (!mine) {
          sendSocket({ type: 'delivered', message_id: m.id });
          if (visible) void api.markRead(m.conversation_id);
        }
        break;
      }
      case 'receipt':
        void refreshConvMessages(data.conversation_id);
        break;
      case 'message_deleted':
        set((st) => ({ msgs: { ...st.msgs, [data.conversation_id]: (st.msgs[data.conversation_id] ?? []).map((m) => (m.id === data.message_id ? { ...m, deleted: true, body: '', attachment: undefined } : m)) } }));
        break;
      case 'conversation':
        void loadConvs();
        break;
      case 'call_incoming': case 'call_answered': case 'call_ended': case 'call_hold': case 'call_transferred':
        useCalls.getState().onEvent(type, data);
        break;
      case 'announcement':
        useAnn.getState().onEvent(data);
        break;
      case 'typing':
        if (data.user_id === s.me) break;
        set((st) => ({ typing: { ...st.typing, [data.conversation_id]: data.user_id } }));
        window.clearTimeout(typingTimers[data.conversation_id]);
        typingTimers[data.conversation_id] = window.setTimeout(
          () => set((st) => { const t = { ...st.typing }; delete t[data.conversation_id]; return { typing: t }; }), 3000);
        break;
    }
  }

  return {
    ...empty,

    start(meId) {
      set({ ...empty, me: meId });
      connectSocket(onEvent, () => { void resync(); void useCalls.getState().loadLog(); });
    },
    stop() {
      disconnectSocket();
      useCalls.getState().reset();
      set({ ...empty });
    },

    async openConv(id) {
      set({ active: id });
      if (!get().msgs[id]) {
        const r = await api.messages(id);
        set((s) => ({ msgs: { ...s.msgs, [id]: r.messages }, hasMore: { ...s.hasMore, [id]: r.has_more } }));
      }
      if (get().convs.find((c) => c.id === id)?.unread) {
        await api.markRead(id);
        set((s) => ({ convs: s.convs.map((c) => (c.id === id ? { ...c, unread: 0 } : c)) }));
      }
    },

    async loadOlder(id) {
      const first = get().msgs[id]?.[0];
      if (!first) return;
      const r = await api.messages(id, first.id);
      set((s) => ({ msgs: { ...s.msgs, [id]: merge(s.msgs[id], r.messages) }, hasMore: { ...s.hasMore, [id]: r.has_more } }));
    },

    async send(id, text, attachmentId) {
      const m = await api.sendMessage(id, text, attachmentId);
      set((s) => ({ msgs: { ...s.msgs, [id]: merge(s.msgs[id], [m]) } }));
    },

    async remove(id, messageId) {
      await api.deleteMessage(messageId);
      set((s) => ({ msgs: { ...s.msgs, [id]: (s.msgs[id] ?? []).map((m) => (m.id === messageId ? { ...m, deleted: true, body: '', attachment: undefined } : m)) } }));
    },

    async startDirect(userId) {
      const c = await api.createConversation([userId]);
      set((s) => ({ convs: s.convs.some((x) => x.id === c.id) ? s.convs : [c, ...s.convs] }));
      await get().openConv(c.id);
    },

    async createGroup(name, ids) {
      const c = await api.createConversation(ids, name);
      set((s) => ({ convs: [c, ...s.convs.filter((x) => x.id !== c.id)], newChatOpen: false }));
      await get().openConv(c.id);
    },

    notifyTyping(id) {
      const now = Date.now();
      if (now - lastTypingSent < 2000) return;
      lastTypingSent = now;
      sendSocket({ type: 'typing', conversation_id: id });
    },

    setNewChat: (newChatOpen) => set({ newChatOpen }),
  };
});

export const convTitle = (c: Conversation, users: Record<number, DirUser>, me: number) =>
  c.name ?? users[c.members.find((id) => id !== me) ?? 0]?.display_name ?? '…';
