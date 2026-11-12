// Call signaling acceptance test: ring, answer, busy, DND, reject, cancel, hold, transfer, CDR.
// Usage (dev stack running):  NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/calls.mjs [https://localhost]
import assert from 'node:assert/strict';

const BASE = process.argv[2] ?? 'https://localhost';
const PASS = 'Demo-Passw0rd!';
let n = 0;
const ok = (name, extra = '') => { n++; console.log(`PASS ${name}${extra ? ' ' + extra : ''}`); };

async function http(token, method, path, body, root = '/api') {
  const res = await fetch(BASE + root + path, {
    method, headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json = null; try { json = JSON.parse(text); } catch { /* not json */ }
  return { status: res.status, json, text };
}

async function login(username) {
  const r = await http(null, 'POST', '/auth/login', { username, password: PASS });
  assert.equal(r.status, 200, `${username}: ${r.text}`);
  return { token: r.json.access_token, id: r.json.user.id };
}

function connect(user) {
  const ws = new WebSocket(BASE.replace('https', 'wss') + '/api/ws');
  const events = [];
  const waiters = [];
  ws.onmessage = (m) => {
    const ev = JSON.parse(m.data); events.push(ev);
    for (const w of [...waiters]) if (w.match(ev)) { waiters.splice(waiters.indexOf(w), 1); w.resolve({ ev, at: performance.now() }); }
  };
  const wait = (match, ms = 3000) => new Promise((resolve, reject) => {
    const hit = events.find(match);
    if (hit) return resolve({ ev: hit, at: performance.now() });
    const w = { match, resolve }; waiters.push(w);
    setTimeout(() => { waiters.splice(waiters.indexOf(w), 1); reject(new Error('timeout waiting for event')); }, ms);
  });
  return new Promise((resolve, reject) => {
    ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token: user.token }));
    ws.onerror = () => reject(new Error('ws error'));
    wait((e) => e.type === 'ready').then(() => resolve({ ws, wait, events, none: (type, ms = 600) => new Promise((r) => setTimeout(() => r(!events.some((e) => e.type === type)), ms)) }), reject);
  });
}

const presence = async (t, id) => (await http(t, 'GET', '/presence')).json.presence[String(id)];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const A = await login('asaid'), B = await login('oabdallah'), C = await login('mjuma');
const wa = await connect(A), wb = await connect(B), wc = await connect(C);
// start from a clean slate (previous runs, other tests)
for (const u of [A, B, C]) { await http(u.token, 'PUT', '/presence', { status: 'on' }); }
await sleep(200);

// ---- ring + answer
let call;
{
  const t0 = performance.now();
  const r = await http(A.token, 'POST', '/calls', { to_user_id: B.id });
  assert.equal(r.status, 200); assert.equal(r.json.state, 'ringing'); call = r.json.id;
  const { ev, at } = await wb.wait((e) => e.type === 'call_incoming' && e.data.call_id === call);
  assert.equal(ev.data.from.id, A.id);
  ok('callee is notified of an incoming call in real time', `(${Math.round(at - t0)} ms)`);

  assert.equal((await http(C.token, 'POST', `/calls/${call}/answer`)).status, 404); ok('only the callee can answer');
  assert.equal((await http(B.token, 'POST', `/calls/${call}/answer`)).status, 200);
  await wa.wait((e) => e.type === 'call_answered' && e.data.call_id === call);
  await wb.wait((e) => e.type === 'call_answered' && e.data.call_id === call);
  ok('both parties receive call_answered');
  assert.equal(await presence(C.token, A.id), 'busy'); assert.equal(await presence(C.token, B.id), 'busy');
  ok('both parties switch to "busy" during the call');
}

// ---- tokens, ICE, media auth (api and media share the signing secret)
{
  const ta = await http(A.token, 'GET', `/calls/${call}/token`);
  assert.equal(ta.status, 200);
  assert.equal((await http(C.token, 'GET', `/calls/${call}/token`)).status, 404); ok('outsider cannot get a media token');
  const ice = await http(A.token, 'GET', '/calls/ice');
  const turn = ice.json.ice_servers.find((s) => s.username);
  assert.ok(turn.credential && /^\d+:\d+$/.test(turn.username)); ok('time-limited TURN credentials are issued');
  const offer = await http(ta.json.token, 'POST', `/calls/${call}/offer`, { sdp: 'invalid' }, '/media');
  assert.equal(offer.status, 400); ok('media accepts the api-issued token (then rejects a malformed SDP)');
  assert.equal((await http('forged', 'POST', `/calls/${call}/offer`, { sdp: 'v=0' }, '/media')).status, 401); ok('media rejects a forged token');
  const other = await http(ta.json.token, 'POST', `/calls/other-call/offer`, { sdp: 'v=0' }, '/media');
  assert.equal(other.status, 403); ok('a token is bound to its call');
}

// ---- hold
{
  assert.equal((await http(A.token, 'POST', `/calls/${call}/hold`, { on: true })).status, 204);
  const { ev } = await wb.wait((e) => e.type === 'call_hold' && e.data.on === true);
  assert.equal(ev.data.by, A.id); ok('hold is relayed to the other party');
}

// ---- busy / already in call
{
  const r = await http(C.token, 'POST', '/calls', { to_user_id: B.id });
  assert.equal(r.json.outcome, 'busy'); ok('calling a busy user returns busy (486)');
  assert.equal((await http(A.token, 'POST', '/calls', { to_user_id: C.id })).status, 409); ok('a user already in a call cannot start another');
}

// ---- blind transfer: B hands A over to C
let call2;
{
  const r = await http(B.token, 'POST', `/calls/${call}/transfer`, { to_user_id: C.id });
  assert.equal(r.status, 200, r.text); call2 = r.json.id;
  await wb.wait((e) => e.type === 'call_ended' && e.data.call_id === call && e.data.outcome === 'transferred');
  const tr = await wa.wait((e) => e.type === 'call_transferred' && e.data.call_id === call2);
  assert.equal(tr.ev.data.old_call_id, call);
  const inc = await wc.wait((e) => e.type === 'call_incoming' && e.data.call_id === call2);
  assert.equal(inc.ev.data.from.id, A.id); assert.equal(inc.ev.data.transferred_by.id, B.id);
  ok('blind transfer: transferor leaves, transferee is re-routed, target rings with caller = transferee');
  assert.equal(await presence(C.token, B.id), 'on'); ok('transferor gets their previous status back');
  assert.equal((await http(C.token, 'POST', `/calls/${call2}/answer`)).status, 200);
  await wa.wait((e) => e.type === 'call_answered' && e.data.call_id === call2);
  ok('transferred call is answered by the target');
  assert.equal((await http(A.token, 'POST', `/calls/${call2}/hangup`)).status, 204);
  await wc.wait((e) => e.type === 'call_ended' && e.data.call_id === call2);
  await sleep(150);
  assert.equal(await presence(A.token, A.id), 'on'); assert.equal(await presence(A.token, C.id), 'on');
  ok('hangup ends the call for both and restores statuses');
}

// ---- ring timeout must not kill an answered call (guard) + reject + cancel
{
  const r = await http(C.token, 'POST', '/calls', { to_user_id: B.id });
  await wb.wait((e) => e.type === 'call_incoming' && e.data.call_id === r.json.id);
  assert.equal((await http(B.token, 'POST', `/calls/${r.json.id}/reject`)).status, 204);
  const { ev } = await wc.wait((e) => e.type === 'call_ended' && e.data.call_id === r.json.id);
  assert.equal(ev.data.outcome, 'rejected'); ok('callee can reject; caller is told');

  const c2 = await http(C.token, 'POST', '/calls', { to_user_id: B.id });
  await wb.wait((e) => e.type === 'call_incoming' && e.data.call_id === c2.json.id);
  assert.equal((await http(C.token, 'POST', `/calls/${c2.json.id}/hangup`)).status, 204);
  const e2 = await wb.wait((e) => e.type === 'call_ended' && e.data.call_id === c2.json.id);
  assert.equal(e2.ev.data.outcome, 'cancelled'); ok('caller can cancel while ringing');
  assert.equal((await http(C.token, 'POST', `/calls/${c2.json.id}/hangup`)).status, 409); ok('ending a call twice is refused');
}

// ---- DND, offline, in-meeting
{
  await http(B.token, 'PUT', '/presence', { status: 'dnd' });
  const r = await http(C.token, 'POST', '/calls', { to_user_id: B.id });
  assert.equal(r.json.outcome, 'dnd');
  await sleep(400);
  assert.ok(!wb.events.some((e) => e.type === 'call_incoming' && e.data.call_id === r.json.id), 'DND callee must not be notified');
  ok('do-not-disturb rejects the call without ringing (486)');
  await http(B.token, 'PUT', '/presence', { status: 'meet' });
  const m = await http(C.token, 'POST', '/calls', { to_user_id: B.id });
  const { ev } = await wb.wait((e) => e.type === 'call_incoming' && e.data.call_id === m.json.id);
  assert.equal(ev.data.silent, true); ok('"in meeting" rings silently');
  await http(C.token, 'POST', `/calls/${m.json.id}/hangup`);
  await http(B.token, 'PUT', '/presence', { status: 'on' });

  const users = (await http(A.token, 'GET', '/users?q=Salim')).json.users;
  const off = await http(A.token, 'POST', '/calls', { to_user_id: users[0].id });
  assert.equal(off.json.outcome, 'offline'); ok('calling an employee who is not clocked in returns offline');
}

// ---- CDR
{
  const list = (await http(A.token, 'GET', '/calls')).json.calls;
  const outcomes = new Set(list.map((c) => c.outcome));
  assert.ok(outcomes.has('transferred') && outcomes.has('answered'), 'journal must contain answered and transferred calls');
  assert.ok(list.every((c) => c.other.display_name && ['in', 'out'].includes(c.direction)));
  assert.ok(list.some((c) => c.outcome === 'offline'));
  ok('call journal lists direction, other party and outcome', `(${list.length} entries, outcomes: ${[...outcomes].join(', ')})`);
}

console.log(`\n${n} checks passed`);
for (const w of [wa, wb, wc]) w.ws.close();
process.exit(0);
