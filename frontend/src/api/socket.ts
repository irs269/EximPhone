import { getToken, refresh } from './client';

export interface ServerEvent { type: string; data: any }

let ws: WebSocket | null = null;
let stopped = true;
let attempt = 0;
let retryTimer: number | undefined;

/** Opens the realtime socket (auth frame first: tokens never travel in the URL) and reconnects with backoff. */
export function connectSocket(onEvent: (e: ServerEvent) => void, onReady: () => void): void {
  stopped = false;
  const open = () => {
    if (stopped) return;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const sock = new WebSocket(`${proto}://${location.host}/api/ws`);
    ws = sock;
    sock.onopen = () => sock.send(JSON.stringify({ type: 'auth', token: getToken() }));
    sock.onmessage = (m) => {
      const ev = JSON.parse(m.data) as ServerEvent;
      if (ev.type === 'ready') { attempt = 0; onReady(); return; }
      onEvent(ev);
    };
    sock.onclose = () => {
      if (stopped) return;
      const delay = Math.min(15000, 500 * 2 ** attempt++);
      // A fresh access token is needed: the previous one may have expired.
      retryTimer = window.setTimeout(() => { void refresh().then(open); }, delay);
    };
  };
  open();
}

export function sendSocket(msg: object): void {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
}

export function disconnectSocket(): void {
  stopped = true;
  window.clearTimeout(retryTimer);
  ws?.close();
  ws = null;
}
