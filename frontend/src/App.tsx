import { useEffect } from 'react';
import { refresh } from './api/client';
import { useApp } from './store/app';
import { Login } from './views/Login';
import { Shell } from './components/Shell';
import { Dialogs } from './components/Dialogs';

export function App() {
  const phase = useApp((s) => s.phase);
  const bootstrap = useApp((s) => s.bootstrap);

  useEffect(() => { void bootstrap(); }, [bootstrap]);

  // Keep the 15-minute access token fresh while the work day is open.
  useEffect(() => {
    if (phase !== 'in') return;
    const id = window.setInterval(() => { void refresh(); }, 10 * 60 * 1000);
    return () => window.clearInterval(id);
  }, [phase]);

  if (phase === 'loading') return null;
  if (phase === 'out') return <><Login /><Dialogs /></>;
  return <Shell />;
}
