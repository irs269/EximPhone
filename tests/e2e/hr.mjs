// Phase 6 acceptance: RBAC, manager scope, HR overview, exports, corrections, validation, announcements, audit.
// Usage (dev stack running, demo data loaded with `make seed`):
//   NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/hr.mjs [https://127.0.0.1]
import assert from 'node:assert/strict';

const BASE = process.argv[2] ?? 'https://127.0.0.1';
const PASS = 'Demo-Passw0rd!';
let n = 0;
const ok = (name, extra = '') => { n++; console.log(`PASS ${name}${extra ? ' ' + extra : ''}`); };

async function api(token, method, path, body) {
  const res = await fetch(BASE + '/api' + path, {
    method, headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const buf = Buffer.from(await res.arrayBuffer());
  let json = null; try { json = JSON.parse(buf.toString()); } catch { /* binary or empty */ }
  return { status: res.status, json, buf, type: res.headers.get('content-type') ?? '' };
}

async function login(username) {
  const r = await api(null, 'POST', '/auth/login', { username, password: PASS });
  assert.equal(r.status, 200, `${username}: ${r.buf.toString()}`);
  return { token: r.json.access_token, id: r.json.user.id, username };
}

function listen(user) {
  const ws = new WebSocket(BASE.replace('https', 'wss') + '/api/ws');
  const events = [];
  ws.onmessage = (m) => events.push(JSON.parse(m.data));
  return new Promise((resolve, reject) => {
    ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token: user.token }));
    ws.onerror = () => reject(new Error('ws error'));
    const t = setInterval(() => { if (events.some((e) => e.type === 'ready')) { clearInterval(t); resolve({ ws, events }); } }, 20);
  });
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const admin = await login('asaid');
const hr = await login('mjuma');
const manager = await login('oabdallah');
const comm = await login('imoussa');
const auditor = await login('hbakari');
const emp = await login('kseif');
const member = await login('nyoussouf'); // in the manager's team
const sockets = await Promise.all([hr, manager, comm, auditor, emp, member].map(listen));

// ---------- RBAC ----------
{
  assert.equal((await api(emp.token, 'GET', '/admin/users')).status, 403); ok('non-admin cannot list users');
  const users = (await api(admin.token, 'GET', '/admin/users')).json.users;
  assert.ok(users.length >= 20); ok('admin lists the directory with roles', `(${users.length} users)`);
  const setRoles = (u, roles) => api(admin.token, 'PUT', `/admin/users/${u.id}/roles`, { roles });
  assert.equal((await setRoles(hr, ['hr'])).status, 204);
  assert.equal((await setRoles(manager, ['manager'])).status, 204);
  assert.equal((await setRoles(comm, ['communication'])).status, 204);
  assert.equal((await setRoles(auditor, ['auditor'])).status, 204);
  assert.equal((await setRoles(emp, [])).status, 204);
  assert.equal((await setRoles(member, [])).status, 204);
  assert.equal((await api(admin.token, 'PUT', `/admin/users/${admin.id}/roles`, { roles: ['hr'] })).status, 400); ok('an admin cannot remove their own it_admin role');
  assert.equal((await setRoles(emp, ['superuser'])).status, 400); ok('unknown role rejected');
  assert.equal((await api(admin.token, 'PUT', `/admin/users/${member.id}/manager`, { manager_id: manager.id })).status, 204);
  assert.equal((await api(admin.token, 'PUT', `/admin/users/${manager.id}/manager`, { manager_id: manager.id })).status, 400); ok('a user cannot be their own manager');
  const me = (await api(hr.token, 'GET', '/me')).json;
  assert.deepEqual(me.roles, ['employee', 'hr']); ok('/me returns the roles');
}

// ---------- sites ----------
{
  const sites = (await api(admin.token, 'GET', '/admin/sites')).json.sites;
  const s = sites[0];
  assert.equal((await api(admin.token, 'PUT', `/admin/sites/${s.id}`, { ...s, ip_ranges: ['not-a-cidr'] })).status, 400); ok('invalid IP range rejected');
  assert.equal((await api(admin.token, 'PUT', `/admin/sites/${s.id}`, { ...s, end_time: '07:00' })).status, 400); ok('invalid working hours rejected');
  assert.equal((await api(admin.token, 'PUT', `/admin/sites/${s.id}`, { ...s })).status, 204); ok('site rules (IP ranges, hours, tolerance) can be edited');
  assert.equal((await api(emp.token, 'PUT', `/admin/sites/${s.id}`, s)).status, 403);
}

// ---------- overview and scope ----------
let teamDay;
{
  assert.equal((await api(emp.token, 'GET', '/hr/overview')).status, 403); ok('an employee cannot open the HR dashboard');
  const all = (await api(hr.token, 'GET', '/hr/overview')).json;
  assert.ok(all.rows.length >= 20);
  assert.equal(all.totals.present + all.totals.late + all.totals.absent, all.rows.length);
  assert.ok(all.by_site.length >= 3 && all.by_department.length >= 3);
  ok('HR sees every employee with totals by site and department', `(${all.rows.length} rows, ${all.totals.present + all.totals.late} present today)`);
  const team = (await api(manager.token, 'GET', '/hr/overview')).json;
  const names = new Set(team.rows.map((r) => r.user_id));
  assert.ok(names.has(manager.id) && names.has(member.id) && names.size === 2, `manager sees ${names.size} people`);
  ok('a manager only sees their own team');
  assert.equal((await api(manager.token, 'GET', '/hr/attendance?from=2000-01-01')).status, 400); // wider than the one year cap
  const h2 = await api(manager.token, 'GET', '/hr/attendance');
  assert.ok(h2.json.days.every((d) => d.user_id === member.id || d.user_id === manager.id)); teamDay = h2.json.days.find((d) => d.user_id === member.id);
  ok('history is scoped for managers and capped to a one year window');
  const dept = all.rows.find((r) => r.department).department;
  const f = (await api(hr.token, 'GET', `/hr/attendance?department=${encodeURIComponent(dept)}`)).json.days;
  assert.ok(f.length > 0 && f.every((d) => d.department === dept)); ok('history can be filtered by department');
  const late = (await api(hr.token, 'GET', '/hr/attendance?status=late')).json.days;
  assert.ok(late.length > 0 && late.every((d) => d.arrival === 'late')); ok('history can be filtered by late arrivals');
}

// ---------- early departure validation ----------
{
  const pending = (await api(hr.token, 'GET', '/hr/early-departures')).json.days;
  assert.ok(pending.length > 0, 'the demo data contains early departures');
  const other = pending.find((d) => d.user_id !== member.id && d.user_id !== manager.id);
  assert.equal((await api(manager.token, 'POST', `/hr/attendance/${other.id}/validate`, { approved: true })).status, 403); ok('a manager cannot validate someone outside their team');
  assert.equal((await api(emp.token, 'POST', `/hr/attendance/${other.id}/validate`, { approved: true })).status, 403);
  assert.equal((await api(hr.token, 'POST', `/hr/attendance/${other.id}/validate`, { approved: false, comment: 'Motif insuffisant' })).status, 204);
  const after = (await api(hr.token, 'GET', '/hr/early-departures?status=all')).json.days.find((d) => d.id === other.id);
  assert.equal(after.early_validation, 'rejected'); ok('HR validates an early departure and it leaves the pending queue');
  const nonEarly = (await api(hr.token, 'GET', '/hr/attendance?status=late')).json.days.find((d) => d.departure === 'normal');
  assert.equal((await api(hr.token, 'POST', `/hr/attendance/${nonEarly.id}/validate`, { approved: true })).status, 409); ok('only early departures can be validated');
}

// ---------- manual correction ----------
let corrected;
{
  const days = (await api(hr.token, 'GET', '/hr/attendance?status=late')).json.days;
  corrected = days.find((d) => d.clock_out);
  const day = corrected.work_date;
  const body = { clock_in: `${day}T05:00:00Z`, clock_out: `${day}T14:30:00Z`, justification: 'Panne du badge, confirmee par le manager' };
  assert.equal((await api(emp.token, 'POST', `/hr/attendance/${corrected.id}/correct`, body)).status, 403);
  assert.equal((await api(manager.token, 'POST', `/hr/attendance/${corrected.id}/correct`, body)).status, 403); ok('only HR can correct a clock-in');
  assert.equal((await api(hr.token, 'POST', `/hr/attendance/${corrected.id}/correct`, { ...body, justification: '' })).status, 400); ok('a correction needs a justification');
  assert.equal((await api(hr.token, 'POST', `/hr/attendance/${corrected.id}/correct`, { ...body, clock_out: `${day}T04:00:00Z` })).status, 400);
  const r = await api(hr.token, 'POST', `/hr/attendance/${corrected.id}/correct`, body);
  assert.equal(r.status, 200, r.buf.toString());
  assert.equal(r.json.arrival, 'on_time'); assert.equal(r.json.duration_min, 570);
  ok('HR correction recalculates the status and the duration', `(${r.json.arrival}, ${r.json.duration_min} min)`);
}

// ---------- exports ----------
{
  const months = [new Date().toISOString().slice(0, 7), new Date(Date.now() - 31 * 864e5).toISOString().slice(0, 7)];
  let csvOk = false;
  for (const month of months) {
    const r = await api(hr.token, 'GET', `/hr/export?month=${month}&format=csv`);
    assert.equal(r.status, 200); assert.ok(r.type.includes('text/csv'));
    const text = r.buf.toString('utf8');
    const lines = text.replace(/^﻿/, '').trim().split('\n');
    if (lines.length > 5) {
      assert.ok(text.startsWith('﻿')); assert.equal(lines[0].split(';').length, 12);
      assert.ok(lines.slice(1).every((l) => l.split(';').length === 12));
      ok('monthly CSV export (UTF-8 BOM, semicolon separated, 12 columns)', `(${lines.length - 1} rows for ${month})`);
      csvOk = true;
      const x = await api(hr.token, 'GET', `/hr/export?month=${month}&format=xlsx`);
      assert.equal(x.status, 200); assert.ok(x.type.includes('spreadsheetml')); assert.equal(x.buf.subarray(0, 2).toString(), 'PK');
      ok('monthly Excel export is a valid .xlsx (zip) file', `(${x.buf.length} bytes)`);
      break;
    }
  }
  assert.ok(csvOk, 'no month with data found: run make seed');
  assert.equal((await api(manager.token, 'GET', `/hr/export?month=${months[0]}`)).status, 403); ok('payroll export is HR only');
  assert.equal((await api(hr.token, 'GET', '/hr/export?month=2026-13')).status, 400);
}

// ---------- announcements ----------
{
  const post = (u, b) => api(u.token, 'POST', '/announcements', b);
  assert.equal((await post(emp, { title: 'x', body: 'y' })).status, 403); ok('an employee cannot publish announcements');
  assert.equal((await post(comm, { title: '', body: 'y' })).status, 400);
  assert.equal((await post(comm, { title: 'x', body: 'y', level: 'critical' })).status, 400);
  const t0 = performance.now();
  const urgent = await post(comm, { title: 'Maintenance du core banking', body: 'Samedi 22h-02h', level: 'urgent' });
  assert.equal(urgent.status, 200); assert.ok(urgent.json.recipients >= 20);
  await sleep(500);
  for (const [i, s] of sockets.entries()) {
    const ev = s.events.find((e) => e.type === 'announcement' && e.data.id === urgent.json.id);
    assert.ok(ev && ev.data.level === 'urgent', `socket ${i} did not receive the urgent announcement`);
  }
  ok('an urgent announcement reaches every connected employee in real time', `(${Math.round(performance.now() - t0)} ms)`);
  const list = (await api(emp.token, 'GET', '/announcements')).json.announcements;
  const mine = list.find((a) => a.id === urgent.json.id);
  assert.equal(mine.acked_at, null); assert.equal(mine.level, 'urgent');
  assert.equal((await api(emp.token, 'POST', `/announcements/${mine.id}/ack`)).status, 204);
  assert.equal((await api(emp.token, 'POST', `/announcements/${mine.id}/ack`)).status, 204); ok('acknowledgement is recorded (and idempotent)');
  assert.ok((await api(emp.token, 'GET', '/announcements')).json.announcements.find((a) => a.id === mine.id).acked_at);
  assert.equal((await api(emp.token, 'GET', `/announcements/${mine.id}/acks`)).status, 403); ok('read receipts are visible to publishers only');
  const acks = (await api(comm.token, 'GET', `/announcements/${mine.id}/acks`)).json;
  assert.equal(acks.total, acks.read.length + acks.unread.length);
  assert.ok(acks.read.some((p) => p.id === emp.id) && acks.unread.some((p) => p.id === hr.id));
  ok('follow-up table shows who has read and who has not', `(${acks.read.length} read, ${acks.unread.length} unread)`);

  // targeting: a department only
  const users = (await api(admin.token, 'GET', '/admin/users')).json.users;
  const me = users.find((u) => u.id === emp.id);
  const other = users.find((u) => u.department && u.department !== me.department && u.id !== admin.id);
  const sites = (await api(admin.token, 'GET', '/admin/sites')).json.sites;
  const deptRows = (await api(hr.token, 'GET', '/hr/overview')).json.rows;
  assert.ok(deptRows.length > 0);
  const deptId = [...new Set(['IT', 'HR', 'Finance', 'Operations', 'Communication'])].indexOf(other.department) + 1;
  const targeted = await post(comm, { title: 'Reservee a un departement', body: 'test', target: { kind: 'department', id: deptId } });
  assert.equal(targeted.status, 200); assert.ok(targeted.json.recipients < urgent.json.recipients);
  const seenByEmp = (await api(emp.token, 'GET', '/announcements')).json.announcements.some((a) => a.id === targeted.json.id);
  assert.equal(seenByEmp, false);
  assert.equal((await api(emp.token, 'POST', `/announcements/${targeted.json.id}/ack`)).status, 404); ok('targeting by department: other departments neither see nor acknowledge it');
  assert.ok(sites.length >= 3);
}

// ---------- audit ----------
{
  assert.equal((await api(emp.token, 'GET', '/audit')).status, 403);
  assert.equal((await api(admin.token, 'GET', '/audit')).status, 403); ok('only the auditor role can read the audit journal');
  const all = (await api(auditor.token, 'GET', '/audit?limit=200')).json.entries;
  const actions = new Set(all.map((e) => e.action));
  for (const a of ['roles_changed', 'attendance_corrected', 'early_departure_validated', 'announcement_published', 'hr_export', 'site_updated', 'login']) {
    assert.ok(actions.has(a), `audit is missing ${a}`);
  }
  ok('sensitive actions are recorded (roles, corrections, validations, announcements, exports, sites)');
  const corr = (await api(auditor.token, 'GET', '/audit?action=attendance_corrected&limit=5')).json.entries[0];
  assert.equal(corr.actor, 'mjuma'); assert.equal(corr.details.justification, 'Panne du badge, confirmee par le manager'); assert.ok(corr.details.old && corr.details.new);
  ok('a correction is audited with who, why, and the old and new values');
  assert.ok((await api(auditor.token, 'GET', '/audit?action=audit_viewed')).json.entries.length > 0); ok('consulting the audit journal is itself audited');
  const del = await fetch(BASE + '/api/audit', { method: 'DELETE', headers: { Authorization: `Bearer ${auditor.token}` } });
  assert.ok([404, 405].includes(del.status)); ok('the audit journal exposes no way to delete or modify entries');
}

// ---------- revocation ----------
{
  assert.equal((await api(admin.token, 'PUT', `/admin/users/${hr.id}/roles`, { roles: [] })).status, 204);
  assert.equal((await api(hr.token, 'GET', '/hr/overview')).status, 403); ok('removing a role takes effect immediately');
  await api(admin.token, 'PUT', `/admin/users/${hr.id}/roles`, { roles: ['hr'] });
}

console.log(`\n${n} checks passed`);
for (const s of sockets) s.ws.close();
process.exit(0);
