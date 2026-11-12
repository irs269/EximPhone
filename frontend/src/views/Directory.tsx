import { useApp } from '../store/app';
import { t } from '../i18n';
import { Icon } from '../components/Icon';
import { Avatar, lineLabel } from '../components/Avatar';
import { people } from '../data/mock';

export function Directory() {
  const { cur, startCall, messageTo } = useApp();
  const p = people.find((x) => 'p' + x.id === cur) ?? people[0];
  return (
    <>
      <div className="mock" role="note">{t('mock.banner')}</div>
      <div className="top"><div className="tt"><b>Annuaire Exim Bank</b><span>{people.length} collaborateurs · synchronisé avec Active Directory</span></div></div>
      <div className="prof">
        <div className="pc">
          <div className="cv" />
          <Avatar p={p} big />
          <div className="info"><b>{p.name}</b><span>{p.role}</span></div>
          <div className="acts">
            <button className="btn" onClick={() => startCall(p)}><Icon name="call" /> Appeler</button>
            <button className="btn gold" onClick={() => messageTo(p.id)}><Icon name="chat" /> Message</button>
          </div>
          <div className="rows">
            <div><span>Poste interne</span><b>{p.ext}</b></div>
            <div><span>Département</span>{p.dept}</div>
            <div><span>Site</span>{p.site}</div>
            <div><span>Statut</span>{lineLabel[p.line]}</div>
            <div><span>E-mail</span>{p.name.toLowerCase().replace(' ', '.')}@eximbank.co.tz</div>
          </div>
        </div>
      </div>
    </>
  );
}
