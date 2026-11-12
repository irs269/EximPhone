import { useApp } from '../store/app';
import { useChat } from '../store/chat';
import { t } from '../i18n';
import { Icon } from '../components/Icon';
import { UserAvatar } from '../components/Avatar';
import { useCalls } from '../store/calls';

const label: Record<string, string> = {
  on: 'status.on', busy: 'status.busy', meet: 'status.meet', away: 'status.away', dnd: 'status.dnd',
};

export function Directory() {
  const { cur, setView } = useApp();
  const dial = useCalls((c) => c.dial);
  const { users, presence, startDirect } = useChat();
  const list = Object.values(users);
  const u = users[Number(cur.slice(1))] ?? list[0];
  if (!u) return <div className="empty">{t('list.none')}</div>;
  const st = presence[u.id];

  return (
    <>
      <div className="top"><div className="tt"><b>Annuaire Exim Bank</b><span>{list.length} collaborateurs · synchronisé avec Active Directory</span></div></div>
      <div className="prof">
        <div className="pc">
          <div className="cv" />
          <UserAvatar u={u} status={st} big />
          <div className="info"><b>{u.display_name}</b><span>{u.department}</span></div>
          <div className="acts">
            {u.extension && <button className="btn" onClick={() => void dial(u.id)}><Icon name="call" /> Appeler</button>}
            <button className="btn gold" onClick={async () => { await startDirect(u.id); setView('chat'); }}><Icon name="chat" /> Message</button>
          </div>
          <div className="rows">
            <div><span>Poste interne</span><b>{u.extension}</b></div>
            <div><span>Département</span>{u.department}</div>
            <div><span>Site</span>{u.site}</div>
            <div><span>Statut</span>{st && label[st] ? t(label[st] as 'status.on') : t('chat.offline')}</div>
            <div><span>E-mail</span>{u.email}</div>
          </div>
        </div>
      </div>
    </>
  );
}
