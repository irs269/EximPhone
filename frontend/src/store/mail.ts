import { create } from 'zustand';
import * as api from '../api/client';
import type { MailAction, MailAttachment, MailCounts, MailFolder, MailItem, MailMessage, MailThread } from '../api/client';
import { t, dateLocale } from '../i18n';
import { useApp } from './app';
import { useChat } from './chat';

const noCounts: MailCounts = { unread_inbox: 0, drafts: 0, starred: 0, external: false };
const ids = (ps: { id: number }[]) => ps.filter((p) => p.id > 0).map((p) => p.id);
const exts = (ps: { id: number; email?: string }[]) => ps.filter((p) => p.id === 0 && p.email).map((p) => p.email as string);

export interface Composer {
  id?: number;
  to: number[]; cc: number[]; bcc: number[];
  to_ext: string[]; cc_ext: string[]; bcc_ext: string[]; // addresses outside the platform
  subject: string; body: string;
  parent_id?: number; forward_of?: number;
  attachments: MailAttachment[];
  showCc: boolean; showBcc: boolean;
  dirty: boolean; busy: boolean; error: string;
}

const blank: Composer = { to: [], cc: [], bcc: [], to_ext: [], cc_ext: [], bcc_ext: [], subject: '', body: '', attachments: [], showCc: false, showBcc: false, dirty: false, busy: false, error: '' };

const stripPrefix = (s: string) => s.replace(/^\s*((re|fwd?|tr)\s*:\s*)+/i, '').trim();
const quote = (m: MailMessage) => {
  const date = new Date(m.date).toLocaleString(dateLocale(), { dateStyle: 'long', timeStyle: 'short' });
  return `\n\n${t('mail.wrote', { date, name: m.from.name })}\n` + m.body.split('\n').map((l) => '> ' + l).join('\n');
};

interface MailState {
  counts: MailCounts;
  folder: MailFolder;
  q: string;
  items: MailItem[];
  more: boolean;
  page: number;
  thread: (MailThread & { folder: MailFolder }) | null;
  composer: Composer | null;
  loadCounts: () => Promise<void>;
  loadList: (reset?: boolean) => Promise<void>;
  loadMore: () => Promise<void>;
  setFolder: (f: MailFolder) => void;
  setQuery: (q: string) => void;
  open: (item: MailItem) => Promise<void>;
  refreshThread: () => Promise<void>;
  act: (action: MailAction, ids: number[], closeThread?: boolean) => Promise<void>;
  compose: (init?: Partial<Composer>) => void;
  reply: (m: MailMessage, all: boolean) => void;
  forward: (m: MailMessage) => void;
  edit: (patch: Partial<Composer>) => void;
  saveDraft: (silent?: boolean) => Promise<void>;
  send: () => Promise<void>;
  discard: () => Promise<void>;
  closeComposer: () => Promise<void>;
  onEvent: (d: { thread_id: number; from: string }) => void;
  reset: () => void;
}

export const useMail = create<MailState>((set, get) => ({
  counts: noCounts, folder: 'inbox', q: '', items: [], more: false, page: 0, thread: null, composer: null,

  async loadCounts() {
    try { set({ counts: await api.mailFolders() }); } catch { /* keep */ }
  },
  async loadList(reset = true) {
    const { folder, q } = get();
    try {
      const r = await api.mailList(folder, q, 0);
      if (get().folder === folder && get().q === q) set({ items: r.items, more: r.more, page: 0 });
    } catch { if (reset) set({ items: [], more: false }); }
  },
  async loadMore() {
    const { folder, q, page, items } = get();
    try {
      const r = await api.mailList(folder, q, page + 1);
      set({ items: [...items, ...r.items], more: r.more, page: page + 1 });
    } catch { /* keep */ }
  },
  setFolder(f) { set({ folder: f, thread: null, items: [], more: false, page: 0 }); void get().loadList(); },
  setQuery(q) { set({ q }); void get().loadList(); },

  async open(item) {
    const folder = get().folder;
    try {
      const th = await api.mailThread(item.thread_id, folder);
      if (folder === 'drafts') {
        const d = th.messages.find((m) => m.id === item.id) ?? th.messages[th.messages.length - 1];
        get().compose({
          id: d.id, to: ids(d.to), cc: ids(d.cc), bcc: ids(d.bcc), to_ext: exts(d.to), cc_ext: exts(d.cc), bcc_ext: exts(d.bcc), subject: d.subject, body: d.body,
          parent_id: d.parent_id ?? undefined, attachments: d.attachments, showCc: d.cc.length > 0, showBcc: d.bcc.length > 0,
        });
        return;
      }
      set({ thread: { ...th, folder } });
      const unread = th.messages.filter((m) => m.unread).map((m) => m.id);
      if (unread.length && folder !== 'trash') {
        await api.mailBatch(unread, 'read');
        set((s) => ({ items: s.items.map((i) => (i.thread_id === item.thread_id ? { ...i, unread: false } : i)) }));
        void get().loadCounts();
      }
    } catch { set({ thread: null }); }
  },
  async refreshThread() {
    const th = get().thread;
    if (!th) return;
    try { set({ thread: { ...(await api.mailThread(th.thread_id, th.folder)), folder: th.folder } }); } catch { set({ thread: null }); }
  },

  async act(action, ids, closeThread = false) {
    try { await api.mailBatch(ids, action); } catch { return; }
    if (closeThread || ['archive', 'trash', 'purge', 'restore'].includes(action)) set({ thread: null });
    else void get().refreshThread();
    await Promise.all([get().loadList(), get().loadCounts()]);
  },

  compose(init) { set({ composer: { ...blank, ...init } }); },
  reply(m, all) {
    const me = useChat.getState().me;
    // answering one's own message goes to its recipients; otherwise to the sender (and, for reply all, the other recipients)
    const ppl = m.mine ? m.to : [m.from, ...(all ? m.to : [])];
    const seen = new Set<string>();
    const uniq = (list: { id: number; email?: string }[]) => list.filter((p) => {
      const k = p.id > 0 ? 'u' + p.id : 'e' + (p.email ?? '').toLowerCase();
      if (seen.has(k) || (p.id === me && !m.mine)) return false;
      seen.add(k);
      return true;
    });
    const to = uniq(ppl);
    const cc = all ? uniq(m.cc) : [];
    get().compose({
      to: ids(to), to_ext: exts(to), cc: ids(cc), cc_ext: exts(cc), showCc: cc.length > 0,
      subject: 'Re: ' + stripPrefix(m.subject), body: quote(m), parent_id: m.id, dirty: true,
    });
  },
  forward(m) {
    const date = new Date(m.date).toLocaleString(dateLocale(), { dateStyle: 'long', timeStyle: 'short' });
    const head = `\n\n${t('mail.fwdHeader')}\n${t('mail.from')} : ${m.from.name}\n${t('mail.subject')} : ${m.subject}\n${date}\n\n${m.body}`;
    get().compose({ subject: 'Fwd: ' + stripPrefix(m.subject), body: head, parent_id: m.id, forward_of: m.id, attachments: [], dirty: true });
  },
  edit(patch) { set((s) => (s.composer ? { composer: { ...s.composer, ...patch, dirty: true, error: '' } } : s)); },

  async saveDraft(silent = false) {
    const c = get().composer;
    if (!c || c.busy) return;
    try {
      const r = await api.mailSaveDraft({
        id: c.id, to: c.to, cc: c.cc, bcc: c.bcc, to_ext: c.to_ext, cc_ext: c.cc_ext, bcc_ext: c.bcc_ext, subject: c.subject, body: c.body,
        parent_id: c.parent_id, forward_of: c.id ? undefined : c.forward_of, attachment_ids: c.attachments.map((a) => a.id),
      });
      set((s) => (s.composer ? { composer: { ...s.composer, id: r.id, attachments: r.attachments, dirty: false } } : s));
      void get().loadCounts();
      if (get().folder === 'drafts') void get().loadList();
      if (!silent) useApp.getState().showToast(t('mail.draftSaved'));
    } catch { /* the user keeps the composer open and can retry */ }
  },

  async send() {
    const c = get().composer;
    if (!c || c.busy) return;
    if (c.to.length + c.cc.length + c.bcc.length + c.to_ext.length + c.cc_ext.length + c.bcc_ext.length === 0) { set({ composer: { ...c, error: t('mail.err.noRecipient') } }); return; }
    set({ composer: { ...c, busy: true, error: '' } });
    try {
      await api.mailSend({
        id: c.id, to: c.to, cc: c.cc, bcc: c.bcc, to_ext: c.to_ext, cc_ext: c.cc_ext, bcc_ext: c.bcc_ext, subject: c.subject, body: c.body,
        parent_id: c.parent_id, forward_of: c.id ? undefined : c.forward_of, attachment_ids: c.attachments.map((a) => a.id),
      });
      set({ composer: null });
      useApp.getState().showToast(t('mail.sent'));
      await Promise.all([get().loadList(), get().loadCounts()]);
      void get().refreshThread();
    } catch (e) {
      const external = e instanceof api.ApiError && /external/i.test(e.message);
      set((s) => (s.composer ? { composer: { ...s.composer, busy: false, error: external ? t('mail.err.external') : t('mail.err.generic') } } : s));
    }
  },

  async discard() {
    const c = get().composer;
    if (c?.id) { try { await api.mailDiscard(c.id); } catch { /* already gone */ } }
    set({ composer: null });
    await Promise.all([get().loadList(), get().loadCounts()]);
  },

  async closeComposer() {
    const c = get().composer;
    if (!c) return;
    const hasContent = c.to.length + c.cc.length + c.bcc.length + c.to_ext.length + c.cc_ext.length + c.bcc_ext.length > 0 || c.subject.trim() !== '' || c.body.trim() !== '' || c.attachments.length > 0;
    if (c.dirty && hasContent) await get().saveDraft(false);
    set({ composer: null });
  },

  onEvent(d) {
    void get().loadCounts();
    if (get().folder === 'inbox' || get().folder === 'starred') void get().loadList();
    if (get().thread?.thread_id === d.thread_id) void get().refreshThread();
    useApp.getState().showToast(t('mail.incoming', { name: d.from }));
  },

  reset: () => set({ counts: noCounts, folder: 'inbox', q: '', items: [], more: false, page: 0, thread: null, composer: null }),
}));
