export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

export interface User { id: number; username: string; display_name: string; extension?: string; site_id: number }
export interface Day {
  id: number; work_date: string; clock_in: string; clock_out?: string;
  arrival: 'on_time' | 'late'; departure?: 'normal' | 'early' | 'not_clocked'; duration_min?: number;
}
export interface LogoutSummary { clock_in: string; clock_out: string; duration_min: number; early: boolean }

let token: string | null = null;

async function request<T>(path: string, init: RequestInit = {}, retry = true): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body) headers.set('Content-Type', 'application/json');
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const res = await fetch('/api' + path, { ...init, headers, credentials: 'same-origin' });
  if (res.status === 401 && retry && !path.startsWith('/auth/')) {
    if (await refresh()) return request<T>(path, init, false);
  }
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, body.error ?? res.statusText);
  return body as T;
}

/** Exchanges the httpOnly refresh cookie for a new short-lived access token (kept in memory only). */
export async function refresh(): Promise<boolean> {
  try {
    const r = await fetch('/api/auth/refresh', { method: 'POST', credentials: 'same-origin' });
    if (!r.ok) return false;
    token = (await r.json()).access_token;
    return true;
  } catch { return false; }
}

export const login = async (username: string, password: string) => {
  const r = await request<{ access_token: string; user: User; attendance: Day; new_clock_in: boolean }>(
    '/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }, false);
  token = r.access_token;
  return r;
};
export const me = () => request<{ user: User; attendance: Day | null }>('/me');
export const history = () => request<{ days: Day[] }>('/me/attendance');
export const logoutSummary = () => request<LogoutSummary>('/auth/logout-summary');
export const logout = async (reason: string) => {
  const r = await request<{ clock_in: string; clock_out: string; duration_min: number; departure: string }>(
    '/auth/logout', { method: 'POST', body: JSON.stringify({ reason }) });
  token = null;
  return r;
};
