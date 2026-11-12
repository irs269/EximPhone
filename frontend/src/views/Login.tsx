import { useState, type FormEvent } from 'react';
import { useApp } from '../store/app';
import { t } from '../i18n';

export function Login() {
  const { login, loginError } = useApp();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    await login(username.trim(), password);
    setBusy(false);
  }

  return (
    <div id="login">
      <div className="brand">
        <img src="/brand/logo-white.svg" alt="Exim Bank" />
        <div>
          <h1>Exim<span>Phone</span></h1>
          <p>{t('login.brandText')}</p>
        </div>
        <div className="feats">
          <div><i />{t('login.f1')}</div><div><i />{t('login.f2')}</div><div><i />{t('login.f3')}</div>
        </div>
      </div>
      <form onSubmit={submit}>
        <h2>{t('login.title')}</h2>
        <p>{t('login.intro')} <b style={{ color: 'var(--navy)' }}>{t('login.warn')}</b></p>
        <label htmlFor="uid">{t('login.user')}</label>
        <input id="uid" className="inp" autoComplete="username" autoFocus required value={username} onChange={(e) => setUsername(e.target.value)} />
        <label htmlFor="pwd">{t('login.password')}</label>
        <input id="pwd" className="inp" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        {loginError && <div className="err" role="alert">{loginError}</div>}
        <button className="btn" type="submit" disabled={busy}>{busy ? t('login.busy') : t('login.submit')}</button>
        <div className="note">{t('login.note')}</div>
      </form>
    </div>
  );
}
