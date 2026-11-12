import { create } from 'zustand';
import * as api from '../api/client';
import type { AnnouncementItem } from '../api/client';

interface AnnState {
  items: AnnouncementItem[];
  urgent: AnnouncementItem | null; // pop-up shown until acknowledged
  load: () => Promise<void>;
  ack: (id: number) => Promise<void>;
  dismissUrgent: () => void;
  onEvent: (data: { id: number; level: string }) => void;
  reset: () => void;
}

export const useAnn = create<AnnState>((set, get) => ({
  items: [], urgent: null,

  async load() {
    try {
      const { announcements } = await api.announcements();
      set({ items: announcements });
      // An urgent announcement not yet acknowledged is shown as a pop-up (also after a reconnection).
      const pending = announcements.find((a) => a.level === 'urgent' && !a.acked_at);
      if (pending && !get().urgent) set({ urgent: pending });
    } catch { /* keep the previous list */ }
  },

  async ack(id) {
    await api.ackAnnouncement(id);
    set((s) => ({
      items: s.items.map((a) => (a.id === id ? { ...a, acked_at: new Date().toISOString() } : a)),
      urgent: s.urgent?.id === id ? null : s.urgent,
    }));
  },

  dismissUrgent: () => set({ urgent: null }),

  onEvent(data) {
    void get().load().then(() => {
      if (data.level === 'urgent') {
        const a = get().items.find((x) => x.id === data.id);
        if (a && !a.acked_at) set({ urgent: a });
      }
    });
  },

  reset: () => set({ items: [], urgent: null }),
}));
