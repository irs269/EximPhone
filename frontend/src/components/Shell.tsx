import { useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { history, searchMessages, type Message } from '../api/client';
import { convTitle, useChat } from '../store/chat';
import { useCalls } from '../store/calls';
import { useAnn } from '../store/ann';
import { Team } from '../views/Team';
import { Audit } from '../views/Audit';
import { NewChatDialog } from './NewChatDialog';
import { useApp, type View } from '../store/app';
import { t } from '../i18n';
import { Icon, type IconName } from './Icon';
import { GroupAvatar, UserAvatar, presenceColor } from './Avatar';
import type { Presence } from '../data/mock';
import { hm, durMin, initials } from '../lib';
import { Chat as ChatView } from '../views/Chat';
import { Phone } from '../views/Phone';
import { Directory } from '../views/Directory';
import { Help, faqIds } from '../views/Help';
import { Tickets, StatusPill } from '../views/Tickets';
import { NewTicketDialog } from './NewTicketDialog';
import { useTickets } from '../store/tickets';
import type { TicketView } from '../api/client';
import { LangSwitch } from './LangSwitch';
import { Announcements } from '../views/Announcements';
import { Attendance, dayStatus } from '../views/Attendance';
import { CallOverlay } from './CallOverlay';
import { Dialogs } from './Dialogs';

const nav: { v: View; icon: IconName; label: Parameters<typeof t>[0] }[] = [
  { v: 'chat', icon: 'chat', label: 'nav.messages' },
  { v: 'phone', icon: 'phone', label: 'nav.calls' },
  { v: 'dir', icon: 'dir', label: 'nav.directory' },
  { v: 'att', icon: 'clock', label: 'nav.attendance' },
  { v: 'ann', icon: 'ann', label: 'nav.announcements' },
  { v: 'tickets', icon: 'ticket', label: 'nav.tickets' },
  { v: 'team', icon: 'chart', label: 'nav.team' },
  { v: 'audit', icon: 'shield', label: 'nav.audit' },
  { v: 'help', icon: 'help', label: 'nav.help' },
];
const visible = (v: View, roles: string[]) =>
  v === 'team' ? roles.includes('hr') || roles.includes('manager') : v === 'audit' ? roles.includes('auditor') : true;

function Rail() {
  const { view, setView, askLogout, user, presence, toggleStatusMenu, roles } = useApp();
  const unread = useChat((c) => c.convs.reduce((n, x) => n + x.unread, 0));
  const pendingAnn = useAnn((a) => a.items.filter((x) => !x.acked_at).length);
  const waitingTickets = useTickets((tk) => tk.summary.waiting);
  const badge = (v: View) => (v === 'chat' ? unread : v === 'ann' ? pendingAnn : v === 'tickets' ? waitingTickets : 0);
  return (
    <nav className="rail" aria-label={t('nav.main')}>
      <div className="mono" title="Exim Bank" role="img" aria-label="Exim Bank" />
      {nav.filter((n) => visible(n.v, roles)).map(({ v, icon, label }) => (
        <button key={v} className={view === v ? 'on' : ''} aria-current={view === v ? 'page' : undefined} onClick={() => setView(v)}>
          <Icon name={icon} />{t(label)}
          {badge(v) > 0 && <span className="badge" aria-label={t('nav.unread', { n: badge(v) })}>{badge(v)}</span>}
        </button>
      ))}
      <button className="lo" style={{ marginTop: 'auto' }} onClick={askLogout}><Icon name="logout" />{t('nav.logout')}</button>
      <button className="me" style={{ marginTop: 8 }} aria-label={t('nav.status')} title={t('nav.status')}
        onClick={(e) => { e.stopPropagation(); toggleStatusMenu(); }}>
        {initials(user?.display_name ?? '?')}
        <span className="dot" style={{ background: presenceColor[presence] }} />
      </button>
    </nav>
  );
}

function StatusMenu() {
  const { statusMenu, presence, setPresence, user, toggleStatusMenu } = useApp();
  useEffect(() => {
    const close = () => toggleStatusMenu(false);
    document.addEventListener('click', close);
    return () => document.removeEventListener('click', close);
  }, [toggleStatusMenu]);
  if (!statusMenu) return null;
  const items: Presence[] = ['on', 'busy', 'meet', 'away', 'dnd'];
  return (
    <div id="stm" onClick={(e) => e.stopPropagation()}>
      <div className="hd"><b>{user?.display_name}</b><span>{t('call.ext', { ext: user?.extension ?? '' })} · {t(`status.${presence}`)}</span></div>
      <LangSwitch />
      {items.map((k) => (
        <button key={k} className={k === presence ? 'sel' : ''} onClick={() => setPresence(k)}>
          <i style={{ background: presenceColor[k] }} /><span><b>{t(`status.${k}`)}</b><small>{t(`status.${k}.d`)}</small></span>
        </button>
      ))}
      <div className="ft">{t('status.footer')}</div>
    </div>
  );
}

function ListPane() {
  const s = useApp();
  const q = s.query.toLowerCase();
  const titles: Record<View, Parameters<typeof t>[0]> = { chat: 'nav.messages', phone: 'nav.calls', dir: 'nav.directory', att: 'nav.attendance', ann: 'nav.announcements', tickets: 'nav.tickets', team: 'nav.team', audit: 'nav.audit', help: 'nav.help' };
  const annItems = useAnn((a) => a.items);
  const chat = useChat();
  const calls = useCalls();
  const [hits, setHits] = useState<Message[]>([]);
  useEffect(() => {
    if (s.view !== 'chat' || q.length < 2) { setHits([]); return; }
    const id = window.setTimeout(() => { void searchMessages(q).then((r) => setHits(r.results)).catch(() => setHits([])); }, 300);
    return () => window.clearTimeout(id);
  }, [q, s.view]);
  const tk = useTickets();
  useEffect(() => { if (s.view === 'tickets') { void tk.loadList(); void tk.loadSummary(); } }, [s.view]); // eslint-disable-line react-hooks/exhaustive-deps
  const days = useQuery({ queryKey: ['history'], queryFn: history, enabled: s.view === 'att' });
  let items: React.ReactNode[] = [];

  if (s.view === 'chat') {
    items = [
      <button key="new" className="btn gold newbtn" onClick={() => chat.setNewChat(true)}>+ {t('chat.new')}</button>,
      ...chat.convs.filter((c) => convTitle(c, chat.users, chat.me).toLowerCase().includes(q)).map((c) => {
        const title = convTitle(c, chat.users, chat.me);
        const other = c.kind === 'direct' ? chat.users[c.members.find((id) => id !== chat.me) ?? 0] : undefined;
        const last = c.last_message;
        return (
          <button key={c.id} className={'it' + (chat.active === c.id ? ' on' : '')} onClick={() => void chat.openConv(c.id)}>
            {other ? <UserAvatar u={other} status={chat.presence[other.id]} /> : <GroupAvatar />}
            <div className="t"><div className="n">{title}<small>{last ? hm(last.created_at) : ''}</small></div>
              <div className="s">{last ? (last.sender_id === chat.me ? t('chat.you') : '') + (last.deleted ? t('chat.deleted') : last.body || last.attachment?.filename || '') : ''}</div></div>
            {c.unread > 0 && <span className="cnt">{c.unread}</span>}
          </button>
        );
      }),
      ...(hits.length ? [<div className="sec" key="res">{t('chat.results')}</div>, ...hits.map((m) => (
        <button key={'r' + m.id} className="it" onClick={() => void chat.openConv(m.conversation_id)}>
          <div className="t"><div className="n">{chat.users[m.sender_id]?.display_name ?? ''}<small>{hm(m.created_at)}</small></div><div className="s">{m.body}</div></div>
        </button>))] : []),
    ];
  } else if (s.view === 'phone') {
    items = [<div className="sec" key="h">{t('list.recent')}</div>, ...calls.log.filter((c) => c.other.display_name.toLowerCase().includes(q)).map((c) => {
      const kind = c.direction === 'out' ? 'out' : c.outcome === 'answered' ? 'in' : 'miss';
      const u = chat.users[c.other.id] ?? { id: c.other.id, display_name: c.other.display_name };
      return (
        <button key={c.id} className="it" onClick={() => void calls.dial(c.other.id)}>
          <UserAvatar u={u} status={chat.presence[c.other.id]} />
          <div className="t"><div className="n"><span className={kind}>{c.other.display_name}</span><small>{hm(c.created_at)}</small></div>
            <div className="s">{kind === 'miss' ? t('call.missed') : kind === 'in' ? t('call.in') : t('call.out')} · {t('call.ext', { ext: c.other.extension ?? '' })}{c.duration_sec ? ` · ${t('call.duration', { m: Math.floor(c.duration_sec / 60), s: c.duration_sec % 60 })}` : ''}</div></div>
        </button>
      );
    })];
  } else if (s.view === 'dir') {
    const all = Object.values(chat.users);
    const depts = [...new Set(all.map((u) => u.department))].sort();
    items = depts.flatMap((d) => {
      const us = all.filter((u) => u.department === d && (u.display_name + u.department + u.extension + u.site).toLowerCase().includes(q));
      return us.length ? [<div className="sec" key={d}>{d}</div>, ...us.map((u) => (
        <button key={u.id} className={'it' + (s.cur === 'u' + u.id ? ' on' : '')} onClick={() => s.setCur('u' + u.id)}>
          <UserAvatar u={u} status={chat.presence[u.id]} /><div className="t"><div className="n">{u.display_name}<small>{u.extension}</small></div><div className="s">{u.site}</div></div>
        </button>))] : [];
    });
  } else if (s.view === 'ann') {
    items = annItems.map((a) => (
      <button key={a.id} className="it" onClick={() => document.getElementById('a' + a.id)?.scrollIntoView({ behavior: 'smooth' })}>
        <div className="av" style={{ background: a.level === 'urgent' ? 'var(--alert)' : 'var(--gold)' }}><Icon name="grid" size={16} /></div>
        <div className="t"><div className="n">{a.level === 'urgent' ? t('ann.urgent') : t('ann.normal')}<small>{hm(a.created_at)}</small></div><div className="s">{a.title}</div></div>
        {!a.acked_at && <span className="cnt">!</span>}
      </button>
    ));
  } else if (s.view === 'team' || s.view === 'audit') {
    items = [];
  } else if (s.view === 'tickets') {
    const filters: [TicketView, Parameters<typeof t>[0]][] = [['', 'tk.filter.all'], ['mine', 'tk.filter.mine'], ['todo', 'tk.filter.todo'], ...(tk.summary.is_head ? [['service', 'tk.filter.service'] as [TicketView, Parameters<typeof t>[0]]] : [])];
    items = [
      <button key="new" className="btn gold newbtn" onClick={() => tk.setComposing(true)}>+ {t('tk.new')}</button>,
      <div className="tkf" key="f" role="group">
        {filters.map(([v, label]) => <button key={v || 'all'} className={tk.view === v ? 'on' : ''} aria-pressed={tk.view === v} onClick={() => tk.setView(v)}>{t(label)}</button>)}
      </div>,
      ...tk.list.filter((x) => (x.title + x.department + x.requester).toLowerCase().includes(q)).map((x) => (
        <button key={x.id} className={'it' + (tk.selected?.id === x.id ? ' on' : '')} onClick={() => void tk.select(x.id)}>
          <div className="t"><div className="n">#{x.id} · {x.title}<small>{hm(x.updated_at)}</small></div>
            <div className="s">{x.department} · {x.requester}</div><div style={{ marginTop: 4 }}><StatusPill s={x.status} /></div></div>
          {(x.can.decide || (x.can.assign && x.status === 'approved') || x.can.start) && <span className="cnt">!</span>}
        </button>
      )),
    ];
    if (!tk.list.length) items = [items[0], items[1], <div className="sec" key="none" style={{ color: 'var(--muted)' }}>{t('tk.none')}</div>];
  } else if (s.view === 'help') {
    items = faqIds.filter((n) => t(`faq.q${n}` as 'faq.q1').toLowerCase().includes(q)).map((n) => (
      <button key={n} className="it" onClick={() => { const d = document.getElementById('faq' + n) as HTMLDetailsElement | null; if (d) { d.open = true; d.scrollIntoView({ behavior: 'smooth' }); } }}>
        <div className="t"><div className="n">{t(`faq.q${n}` as 'faq.q1')}</div></div>
      </button>
    ));
  } else {
    items = [<div className="sec" key="h">{t('list.month')}</div>, ...(days.data?.days ?? []).map((d) => {
      const st = dayStatus(d);
      const bg = st.cls === 'ok' ? 'var(--ok)' : st.cls === 'late' ? 'var(--warn)' : 'var(--alert)';
      return (
        <div className="it" key={d.id}>
          <div className="av" style={{ background: bg }}>{d.work_date.slice(8)}</div>
          <div className="t"><div className="n">{d.work_date}<small>{d.duration_min != null ? durMin(d.duration_min) : '—'}</small></div>
            <div className="s">{hm(d.clock_in)} → {d.clock_out ? hm(d.clock_out) : '—'}</div></div>
        </div>
      );
    })];
  }

  return (
    <aside className="list">
      <header>
        <h3>{t(titles[s.view])}</h3>
        <input className="search" aria-label={t('list.search')} placeholder={t('list.search')} value={s.query} onChange={(e) => s.setQuery(e.target.value)} />
      </header>
      <div className="items">{items.length ? items : <div className="sec" style={{ color: 'var(--muted)' }}>{t('list.none')}</div>}</div>
    </aside>
  );
}

export function Shell() {
  const { view, toast } = useApp();
  // Warn before closing the window while a work day is open.
  useEffect(() => {
    const h = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = ''; };
    window.addEventListener('beforeunload', h);
    return () => window.removeEventListener('beforeunload', h);
  }, []);
  return (
    <>
      <div id="app">
        <Rail />
        <ListPane />
        <main className="main">
          {view === 'chat' && <ChatView />}
          {view === 'phone' && <Phone />}
          {view === 'dir' && <Directory />}
          {view === 'att' && <Attendance />}
          {view === 'ann' && <Announcements />}
          {view === 'team' && <Team />}
          {view === 'audit' && <Audit />}
          {view === 'help' && <Help />}
          {view === 'tickets' && <Tickets />}
        </main>
      </div>
      <StatusMenu />
      <CallOverlay />
      <NewChatDialog />
      <NewTicketDialog />
      <UrgentDialog />
      <Dialogs />
      <div id="toast" className={toast ? 'show' : ''} role="status" aria-live="polite">{toast}</div>
    </>
  );
}



function UrgentDialog() {
  const urgent = useAnn((a) => a.urgent);
  const ack = useAnn((a) => a.ack);
  const dismiss = useAnn((a) => a.dismissUrgent);
  if (!urgent) return null;
  return (
    <div id="dlg" role="presentation">
      <div className="dbox" role="alertdialog" aria-modal="true" aria-label={t('ann.urgentTitle')}>
        <div className="dh" style={{ background: 'linear-gradient(150deg, var(--alert-dark), var(--alert))' }}>
          <div className="ck" style={{ background: 'var(--surface)', color: 'var(--alert)' }} aria-hidden>!</div>
          <b>{t('ann.urgentTitle')}</b><span>{urgent.author}</span>
        </div>
        <div className="bd" style={{ textAlign: 'left' }}>
          <h4 style={{ marginBottom: 8 }}>{urgent.title}</h4>
          <p style={{ whiteSpace: 'pre-wrap', lineHeight: 1.5 }}>{urgent.body}</p>
        </div>
        <div className="bt">
          <button className="btn ghost" onClick={dismiss}>{t('out.cancel')}</button>
          <button className="btn" autoFocus onClick={() => void ack(urgent.id)}>{t('ann.ack')}</button>
        </div>
      </div>
    </div>
  );
}
