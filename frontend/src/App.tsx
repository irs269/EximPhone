import { Fragment, useEffect } from 'react';
import { refresh } from './api/client';
import { useApp } from './store/app';
import { Login } from './views/Login';
import { Shell } from './components/Shell';
import { Dialogs } from './components/Dialogs';
import { useLocale } from './i18n';

export function App() {
  const phase = useApp((s) => s.phase);
  const bootstrap = useApp((s) => s.bootstrap);
  const locale = useLocale((s) => s.locale);

  useEffect(() => { void bootstrap(); }, [bootstrap]);

  // Keep the 15-minute access token fresh while the work day is open.
  useEffect(() => {
    if (phase !== 'in') return;
    const id = window.setInterval(() => { void refresh(); }, 10 * 60 * 1000);
    return () => window.clearInterval(id);
  }, [phase]);

  if (phase === 'loading') return null;
  // The key remounts the tree so every t() call is re-evaluated when the language changes.
  return (
    <Fragment key={locale}>
      {phase === 'out' ? <><Login /><Dialogs /></> : <Shell />}
    </Fragment>
  );
}
