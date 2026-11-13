import { selectable, setLocale, t, useLocale } from '../i18n';

/** Compact FR | EN toggle. Changing the language re-renders the whole app (see App.tsx). */
export function LangSwitch({ className = '' }: { className?: string }) {
  const current = useLocale((s) => s.locale);
  return (
    <div className={'langsw ' + className} role="group" aria-label={t('lang.label')}>
      {selectable.map((l) => (
        <button key={l} type="button" className={l === current ? 'on' : ''} aria-pressed={l === current} lang={l}
          title={t(`lang.${l}`)} onClick={() => setLocale(l)}>{l.toUpperCase()}</button>
      ))}
    </div>
  );
}
