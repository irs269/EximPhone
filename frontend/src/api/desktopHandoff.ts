import { desktopCode } from './client';

/** The PC application registers the eximconnect:// scheme; this link carries only a one-time code (never a password or a token). */
export const signinLink = (code: string) => `eximconnect://signin?code=${encodeURIComponent(code)}`;

export const inDesktopApp = () => !!(window as unknown as { eximDesktop?: unknown }).eximDesktop;

/** Hands the current web session over to the PC application: it opens already signed in. Returns false if the code could not be issued. */
export async function openInDesktopApp(): Promise<boolean> {
  try {
    const r = await desktopCode();
    window.location.href = signinLink(r.code);
    return true;
  } catch { return false; }
}

const FLAG = 'exim.desktopLogin';
/** The PC application opens the browser on /?desktop_login=1: once the person is signed in here, the session goes back to the app. */
export function rememberHandoffRequest(): void { try { sessionStorage.setItem(FLAG, '1'); } catch { /* storage unavailable */ } }
export function takeHandoffRequest(): boolean {
  try { const v = sessionStorage.getItem(FLAG) === '1'; sessionStorage.removeItem(FLAG); return v; } catch { return false; }
}
