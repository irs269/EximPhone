import { useQuery } from '@tanstack/react-query';
import { useEffect } from 'react';
import { history } from '../api/client';
import { useApp, type View } from '../store/app';
import { t } from '../i18n';
import { Icon, type IconName } from './Icon';
import { Avatar, GroupAvatar, presenceColor } from './Avatar';
import { person, people, type Presence } from '../data/mock';
import { hm, durMin, initials } from '../lib';
import { Chat as ChatView } from '../views/Chat';
import { Phone } from '../views/Phone';
import { Directory } from '../views/Directory';
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
];

function Rail() {
  const { view, setView, askLogout, user, presence, toggleStatusMenu, chats, anns } = useApp();
  const unread = chats.reduce((n, c) => n + c.unread, 0);
  const pendingAnn = anns.filter((a) => !a.ack).length;
  const badge = (v: View) => (v === 'chat' ? unread : v === 'ann' ? pendingAnn : 0);
  return (
    <nav className="rail" aria-label="Navigation principale">
      <div className="mono" title="Exim Bank" role="img" aria-label="Exim Bank" />
      {nav.map(({ v, icon, label }) => (
        <button key={v} className={view === v ? 'on' : ''} aria-current={view === v ? 'page' : undefined} onClick={() => setView(v)}>
          <Icon name={icon} />{t(label)}
          {badge(v) > 0 && <span className="badge" aria-label={`${badge(v)} non lus`}>{badge(v)}</span>}
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
      <div className="hd"><b>{user?.display_name}</b><span>Poste {user?.extension} · {t(`status.${presence}`)}</span></div>
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
  const titles: Record<View, Parameters<typeof t>[0]> = { chat: 'nav.messages', phone: 'nav.calls', dir: 'nav.directory', att: 'nav.attendance', ann: 'nav.announcements' };
  const days = useQuery({ queryKey: ['history'], queryFn: history, enabled: s.view === 'att' });
  let items: React.ReactNode[] = [];

  if (s.view === 'chat') {
    items = s.chats.filter((c) => (c.group ?? person(c.person!).name).toLowerCase().includes(q)).map((c) => {
      const p = c.person ? person(c.person) : null;
      const last = c.msgs[c.msgs.length - 1];
      return (
        <button key={c.id} className={'it' + (s.cur === c.id ? ' on' : '')} onClick={() => s.openChat(c.id)}>
          {p ? <Avatar p={p} /> : <GroupAvatar />}
          <div className="t"><div className="n">{c.group ?? p!.name}<small>{last?.time}</small></div>
            <div className="s">{last?.from === 'me' ? 'Vous : ' : ''}{last?.text}</div></div>
          {c.unread > 0 && <span className="cnt">{c.unread}</span>}
        </button>
      );
    });
  } else if (s.view === 'phone') {
    items = [<div className="sec" key="h">{t('list.recent')}</div>, ...s.calls.filter((c) => person(c.person).name.toLowerCase().includes(q)).map((c, i) => {
      const p = person(c.person);
      return (
        <button key={i} className="it" onClick={() => s.startCall(p)}>
          <Avatar p={p} />
          <div className="t"><div className="n"><span className={c.kind}>{p.name}</span><small>{c.when}</small></div>
            <div className="s">{c.kind === 'miss' ? '↙ Manqué' : c.kind === 'in' ? '↙ Entrant' : '↗ Sortant'} · Poste {p.ext}</div></div>
        </button>
      );
    })];
  } else if (s.view === 'dir') {
    const depts = [...new Set(people.map((p) => p.dept))];
    items = depts.flatMap((d) => {
      const ps = people.filter((p) => p.dept === d && (p.name + p.role + p.ext + p.site).toLowerCase().includes(q));
      return ps.length ? [<div className="sec" key={d}>{d}</div>, ...ps.map((p) => (
        <button key={p.id} className={'it' + (s.cur === 'p' + p.id ? ' on' : '')} onClick={() => s.setCur('p' + p.id)}>
          <Avatar p={p} /><div className="t"><div className="n">{p.name}<small>{p.ext}</small></div><div className="s">{p.role}</div></div>
        </button>))] : [];
    });
  } else if (s.view === 'ann') {
    items = s.anns.map((a) => (
      <button key={a.id} className="it" onClick={() => document.getElementById('a' + a.id)?.scrollIntoView({ behavior: 'smooth' })}>
        <div className="av" style={{ background: a.urgent ? 'var(--alert)' : 'var(--gold)' }}><Icon name="grid" size={16} /></div>
        <div className="t"><div className="n">{a.tag}<small>{a.date.split(',')[0]}</small></div><div className="s">{a.title}</div></div>
        {!a.ack && <span className="cnt">!</span>}
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
        </main>
      </div>
      <StatusMenu />
      <CallOverlay />
      <Dialogs />
      <div id="toast" className={toast ? 'show' : ''} role="status" aria-live="polite">{toast}</div>
    </>
  );
}


