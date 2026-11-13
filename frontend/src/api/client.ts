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
export const getToken = () => token;

// ---- directory, presence, chat ----
export interface DirUser { id: number; username: string; display_name: string; extension: string; department: string; site: string; email: string }
export interface Attachment { id: string; filename: string; mime: string; size: number }
export interface Message {
  id: number; conversation_id: number; sender_id: number; body: string; created_at: string;
  deleted?: boolean; status?: 'sent' | 'delivered' | 'read'; attachment?: Attachment;
}
export interface Conversation { id: number; kind: 'direct' | 'group'; name?: string; members: number[]; unread: number; last_message?: Message }

export const users = (q = '') => request<{ users: DirUser[] }>('/users?q=' + encodeURIComponent(q));
export const getPresence = () => request<{ presence: Record<string, string> }>('/presence');
export const putPresence = (status: string) => request<{ status: string }>('/presence', { method: 'PUT', body: JSON.stringify({ status }) });
export const conversations = () => request<{ conversations: Conversation[] }>('/conversations');
export const createConversation = (member_ids: number[], name?: string) =>
  request<Conversation>('/conversations', { method: 'POST', body: JSON.stringify({ member_ids, name }) });
export const messages = (conv: number, before = 0) =>
  request<{ messages: Message[]; has_more: boolean }>(`/conversations/${conv}/messages?limit=50${before ? '&before=' + before : ''}`);
export const sendMessage = (conv: number, body: string, attachment_id?: string) =>
  request<Message>(`/conversations/${conv}/messages`, { method: 'POST', body: JSON.stringify({ body, attachment_id }) });
export const markRead = (conv: number) => request<unknown>(`/conversations/${conv}/read`, { method: 'POST' });
export const deleteMessage = (id: number) => request<unknown>(`/messages/${id}`, { method: 'DELETE' });
export const searchMessages = (q: string) => request<{ results: Message[] }>('/search?q=' + encodeURIComponent(q));

export async function uploadAttachment(conv: number, file: File): Promise<Attachment> {
  const form = new FormData();
  form.set('conversation_id', String(conv));
  form.set('file', file);
  const res = await fetch('/api/attachments', { method: 'POST', body: form, headers: { Authorization: `Bearer ${token}` }, credentials: 'same-origin' });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, body.error ?? res.statusText);
  return body as Attachment;
}

/** Downloads an attachment (auth header required, so we go through fetch) and returns an object URL. */
export async function attachmentUrl(id: string): Promise<string> {
  const res = await fetch('/api/attachments/' + id, { headers: { Authorization: `Bearer ${token}` }, credentials: 'same-origin' });
  if (!res.ok) throw new ApiError(res.status, 'download failed');
  return URL.createObjectURL(await res.blob());
}

// ---- calls ----
export interface Party { id: number; display_name: string; extension: string }
export interface CallLogEntry {
  id: string; direction: 'in' | 'out'; other: Party; outcome: string; state: string; created_at: string; duration_sec: number;
}
export interface CreatedCall { id: string; state: 'ringing' | 'ended'; outcome?: string; to?: Party }

export const createCall = (to_user_id: number) =>
  request<CreatedCall>('/calls', { method: 'POST', body: JSON.stringify({ to_user_id }) });
export const answerCall = (id: string) => request<unknown>(`/calls/${id}/answer`, { method: 'POST' });
export const hangupCall = (id: string) => request<unknown>(`/calls/${id}/hangup`, { method: 'POST' });
export const holdCall = (id: string, on: boolean) => request<unknown>(`/calls/${id}/hold`, { method: 'POST', body: JSON.stringify({ on }) });
export const transferCall = (id: string, to_user_id: number) =>
  request<{ id: string }>(`/calls/${id}/transfer`, { method: 'POST', body: JSON.stringify({ to_user_id }) });
export const callLog = () => request<{ calls: CallLogEntry[] }>('/calls');
export const iceServers = () => request<{ ice_servers: RTCIceServer[] }>('/calls/ice');
export const mediaToken = (id: string) => request<{ token: string }>(`/calls/${id}/token`);

/** WebRTC offer/answer against the media service (non-trickle ICE). */
export async function mediaOffer(callId: string, mediaJwt: string, sdp: string): Promise<string> {
  const res = await fetch(`/media/calls/${callId}/offer`, {
    method: 'POST', headers: { Authorization: `Bearer ${mediaJwt}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ sdp }),
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, body.error ?? 'media error');
  return body.sdp as string;
}

export const me = () => request<{ user: User; attendance: Day | null; roles: string[] }>('/me');

// ---- announcements ----
export interface AnnouncementItem {
  id: number; title: string; body: string; level: 'normal' | 'urgent'; author: string; created_at: string;
  target_kind: string; target_name: string; acked_at: string | null;
}
export interface AckPerson { id: number; display_name: string; department: string; site: string; acked_at: string | null }
export interface OrgMeta { departments: { id: number; name: string }[]; sites: { id: number; name: string }[] }

export const announcements = () => request<{ announcements: AnnouncementItem[] }>('/announcements');
export const ackAnnouncement = (id: number) => request<unknown>(`/announcements/${id}/ack`, { method: 'POST' });
export const announcementAcks = (id: number) =>
  request<{ total: number; read: AckPerson[]; unread: AckPerson[] }>(`/announcements/${id}/acks`);
export const publishAnnouncement = (b: { title: string; body: string; level: string; target: { kind: string; id?: number } }) =>
  request<{ id: number; recipients: number }>('/announcements', { method: 'POST', body: JSON.stringify(b) });
export const orgMeta = () => request<OrgMeta>('/meta/org');

// ---- HR / managers ----
export interface OverviewRow {
  user_id: number; display_name: string; department: string; site: string; status: 'present' | 'late' | 'absent';
  day_id?: number; clock_in?: string; clock_out?: string; departure?: string;
}
export interface Bucket { name: string; present: number; late: number; absent: number }
export interface Overview { date: string; weekend: boolean; totals: Bucket; by_site: Bucket[]; by_department: Bucket[]; rows: OverviewRow[] }
export interface DayRow {
  id: number; user_id: number; display_name: string; username: string; department: string; site: string; work_date: string;
  clock_in: string; clock_out: string | null; arrival: string; departure: string; duration_min: number | null;
  early_reason: string; early_validation: string; validation_comment: string;
}
export const hrOverview = (date = '') => request<Overview>('/hr/overview' + (date ? '?date=' + date : ''));
export const hrAttendance = (qs: Record<string, string>) =>
  request<{ days: DayRow[] }>('/hr/attendance?' + new URLSearchParams(Object.entries(qs).filter(([, v]) => v)).toString());
export const hrEarly = () => request<{ days: DayRow[] }>('/hr/early-departures');
export const hrValidate = (id: number, approved: boolean, comment: string) =>
  request<unknown>(`/hr/attendance/${id}/validate`, { method: 'POST', body: JSON.stringify({ approved, comment }) });
export const hrCorrect = (id: number, b: { clock_in?: string; clock_out?: string; justification: string }) =>
  request<unknown>(`/hr/attendance/${id}/correct`, { method: 'POST', body: JSON.stringify(b) });

/** Downloads the monthly payroll export (auth header required, so through fetch) and saves it. */
export async function downloadExport(month: string, format: 'csv' | 'xlsx'): Promise<void> {
  const res = await fetch(`/api/hr/export?month=${month}&format=${format}`, { headers: { Authorization: `Bearer ${token}` }, credentials: 'same-origin' });
  if (!res.ok) throw new ApiError(res.status, 'export failed');
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url; a.download = `pointages-${month}.${format}`; a.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 5000);
}

// ---- audit ----
export interface AuditEntry { id: number; at: string; actor: string; action: string; ip: string; details: Record<string, unknown> }
export const auditLog = (qs: Record<string, string>) =>
  request<{ entries: AuditEntry[] }>('/audit?' + new URLSearchParams(Object.entries(qs).filter(([, v]) => v)).toString());
export const history = () => request<{ days: Day[] }>('/me/attendance');
export const logoutSummary = () => request<LogoutSummary>('/auth/logout-summary');
export const logout = async (reason: string) => {
  const r = await request<{ clock_in: string; clock_out: string; duration_min: number; departure: string }>(
    '/auth/logout', { method: 'POST', body: JSON.stringify({ reason }) });
  token = null;
  return r;
};

// ---- tickets ----
export type TicketStatus = 'pending' | 'approved' | 'rejected' | 'assigned' | 'in_progress' | 'resolved' | 'cancelled';
export type TicketPriority = 'low' | 'normal' | 'high' | 'urgent';
export interface TicketCan { decide: boolean; assign: boolean; start: boolean; resolve: boolean; cancel: boolean }
export interface TicketEvent { kind: string; actor: string; comment: string; created_at: string }
export interface Ticket {
  id: number; title: string; description: string; priority: TicketPriority; status: TicketStatus;
  requester_id: number; requester: string; department_id: number; department: string;
  assignee_id: number | null; assignee: string; decision_comment: string; decided_by: string; resolution: string;
  created_at: string; updated_at: string; can: TicketCan; events?: TicketEvent[];
}
export interface TicketSummary { to_decide: number; to_assign: number; to_do: number; mine_open: number; waiting: number; is_head: boolean }
export interface TicketService { id: number; name: string; heads: string[] }
export type TicketView = '' | 'todo' | 'mine' | 'service';

const post = <T>(path: string, body?: unknown) => request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) });
export const tickets = (view: TicketView) => request<{ tickets: Ticket[] }>('/tickets' + (view ? `?view=${view}` : ''));
export const ticket = (id: number) => request<Ticket>(`/tickets/${id}`);
export const ticketSummary = () => request<TicketSummary>('/tickets/summary');
export const ticketServices = () => request<{ services: TicketService[] }>('/tickets/services');
export const ticketCandidates = (id: number) =>
  request<{ candidates: { id: number; display_name: string; extension: string }[] }>(`/tickets/${id}/candidates`);
export const createTicket = (b: { title: string; description: string; priority: TicketPriority; department_id: number }) => post<Ticket>('/tickets', b);
export const decideTicket = (id: number, approve: boolean, comment: string) => post<Ticket>(`/tickets/${id}/decision`, { approve, comment });
export const assignTicket = (id: number, user_id: number, comment: string) => post<Ticket>(`/tickets/${id}/assign`, { user_id, comment });
export const startTicket = (id: number) => post<Ticket>(`/tickets/${id}/start`);
export const resolveTicket = (id: number, comment: string) => post<Ticket>(`/tickets/${id}/resolve`, { comment });
export const cancelTicket = (id: number) => post<Ticket>(`/tickets/${id}/cancel`);
export const commentTicket = (id: number, comment: string) => post<{ ok: boolean }>(`/tickets/${id}/comments`, { comment });
