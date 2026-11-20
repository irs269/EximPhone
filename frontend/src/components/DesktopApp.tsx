import { useEffect, useState } from 'react';
import { t } from '../i18n';
import { Icon } from './Icon';

interface Manifest { file: string; version: string; size: number; sha256: string; published_at?: string }

// One request per page load; a server that publishes no installer answers 404 and the card simply stays hidden.
let cached: Promise<Manifest | null> | null = null;
const loadManifest = () => (cached ??= fetch('/download/manifest.json', { cache: 'no-store' })
  .then((r) => (r.ok ? (r.json() as Promise<Manifest>) : null))
  .catch(() => null));

const inDesktopApp = () => !!(window as unknown as { eximDesktop?: unknown }).eximDesktop;

/** "PC application": a download button in the profile menu (compact) or the full explanation in the help page. */
export function DesktopApp({ compact = false }: { compact?: boolean }) {
  const [m, setM] = useState<Manifest | null>(null);
  useEffect(() => { void loadManifest().then(setM); }, []);

  if (inDesktopApp()) {
    return compact ? null : <div className="dsk"><Icon name="pc" /><div><b>{t('desk.title')}</b><small>{t('desk.installed')}</small></div></div>;
  }
  if (!m) return null;
  const windows = /Windows/i.test(navigator.userAgent);
  const size = Math.max(1, Math.round(m.size / (1 << 20)));
  return (
    <div className={'dsk' + (compact ? ' compact' : '')}>
      <Icon name="pc" />
      <div>
        <b>{t('desk.title')}</b>
        <small>{t('desk.sub', { version: m.version, size })}</small>
        {!compact && <p>{t('desk.why')}</p>}
        {!compact && windows && <p>{t('desk.steps')}</p>}
        {!compact && <p className="dskhash">{t('desk.hash', { hash: m.sha256 })}</p>}
      </div>
      {windows
        ? <a className="btn" href={`/download/${m.file}`} download>{t('desk.download')}</a>
        : <small>{t('desk.windowsOnly')}</small>}
    </div>
  );
}
