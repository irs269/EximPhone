// Meetings and video calls: invitations, tokens, the SFU signalling WebSocket through nginx, leaving, ending.
// (Browser media itself is covered by the Go test services/media/meeting_test.go.)
// Usage:  NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/meetings.mjs [https://localhost]
import assert from 'node:assert/strict';

const BASE = process.argv[2] ?? 'https://localhost';
const PASS = 'Demo-Passw0rd!';
let n = 0;
const ok = (name) => { n++; console.log(`PASS ${name}`); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function http(token, method, path, body) {
  const res = await fetch(BASE + '/api' + path, {
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
  return { name: username, token: r.json.access_token, id: r.json.user.id };
}
function connect(user) { // the platform WebSocket (notifications)
  const ws = new WebSocket(BASE.replace('https', 'wss') + '/api/ws');
  const events = []; const waiters = [];
  ws.onmessage = (m) => {
    const ev = JSON.parse(m.data); events.push(ev);
    for (const w of [...waiters]) if (w.match(ev)) { waiters.splice(waiters.indexOf(w), 1); w.resolve(ev); }
  };
  const wait = (match, ms = 4000) => new Promise((resolve, reject) => {
    const hit = events.find(match); if (hit) return resolve(hit);
    const w = { match, resolve }; waiters.push(w);
    setTimeout(() => { waiters.splice(waiters.indexOf(w), 1); reject(new Error('timeout waiting for event')); }, ms);
  });
  return new Promise((resolve, reject) => {
    ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token: user.token }));
    ws.onerror = () => reject(new Error('ws error'));
    wait((e) => e.type === 'ready').then(() => resolve({ ws, wait, events }), reject);
  });
}
// the SFU signalling socket (through nginx), as the browser opens it
async function joinMedia(user, meetingId) {
  const t = await http(user.token, 'POST', `/meetings/${meetingId}/token`);
  if (t.status !== 200) return { status: t.status };
  const ws = new WebSocket(BASE.replace('https', 'wss') + `/media/meetings/${meetingId}/ws`);
  const msgs = []; let closed = null;
  ws.onmessage = (m) => msgs.push(JSON.parse(m.data));
  ws.onclose = (e) => { closed = e.code; };
  await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = () => reject(new Error('media ws error')); });
  ws.send(JSON.stringify({ type: 'join', token: t.json.token }));
  const until = async (fn, ms = 4000) => { const end = Date.now() + ms; while (Date.now() < end) { const v = fn(); if (v) return v; await sleep(50); } throw new Error('timeout'); };
  await until(() => msgs.find((m) => m.type === 'joined'));
  return { status: 200, ws, msgs, closed: () => closed, until, token: t.json };
}
const meeting = async (u, id) => (await http(u.token, 'GET', `/meetings/${id}`));
const statusOf = (m, uid) => m.participants.find((p) => p.id === uid)?.status;

const A = await login('nyoussouf'), B = await login('oabdallah'), C = await login('imoussa'), D = await login('kseif');
const wB = await connect(B), wC = await connect(C), wD = await connect(D), wA = await connect(A);

// ---- creation and invitations
let M;
{
  assert.equal((await http(A.token, 'POST', '/meetings', { kind: 'video_call', invitees: [B.id, C.id] })).status, 400); ok('a video call is for exactly one person');
  assert.equal((await http(A.token, 'POST', '/meetings', { invitees: [999999] })).status, 400); ok('an unknown invitee is refused');
  assert.equal((await http(A.token, 'POST', '/meetings', { invitees: Array.from({ length: 12 }, (_, i) => 100000 + i) })).status, 400); ok('a meeting is limited to 12 people');
  assert.equal((await http(A.token, 'POST', '/meetings', { title: 'x'.repeat(201), invitees: [B.id] })).status, 400);
  const r = await http(A.token, 'POST', '/meetings', { title: 'Point hebdomadaire', invitees: [B.id, C.id] });
  assert.equal(r.status, 200, r.text); M = r.json;
  assert.equal(M.title, 'Point hebdomadaire'); assert.equal(M.host_id, A.id); assert.equal(M.participants.length, 3);
  const inv = await wB.wait((e) => e.type === 'meeting_invite' && e.data.id === M.id);
  assert.equal(inv.data.host_id, A.id); assert.ok(inv.data.host);
  await wC.wait((e) => e.type === 'meeting_invite' && e.data.id === M.id); ok('invitees are notified in real time with the host');
  assert.ok(!wA.events.some((e) => e.type === 'meeting_invite')); ok('the host is not invited to their own meeting');
  assert.equal((await meeting(D, M.id)).status, 404); ok('someone not invited cannot see the meeting');
  assert.equal((await meeting(B, M.id)).json.title, 'Point hebdomadaire');
  assert.equal((await http(D.token, 'POST', `/meetings/${M.id}/token`)).status, 404); ok('nor get a media token');
  assert.equal((await http(B.token, 'GET', '/meetings/not-a-uuid')).status, 404);
  const def = await http(A.token, 'POST', '/meetings', { invitees: [] });
  assert.equal(def.json.title, 'Réunion'); await http(A.token, 'POST', `/meetings/${def.json.id}/end`); ok('a meeting gets a default title');
}

// ---- joining the media room through nginx
let mediaA, mediaB;
{
  mediaA = await joinMedia(A, M.id);
  assert.equal(mediaA.status, 200); ok('the host opens the media room (WebSocket through nginx) with the token from the api');
  mediaB = await joinMedia(B, M.id);
  const joined = mediaB.msgs.find((m) => m.type === 'joined');
  assert.equal(joined.you, B.id); assert.deepEqual(joined.peers.map((p) => p.user), [A.id]); ok('an invitee joins and sees who is already there');
  await mediaA.until(() => mediaA.msgs.find((m) => m.type === 'peer_joined' && m.user === B.id)); ok('the others are told when somebody joins');
  assert.equal(statusOf((await meeting(A, M.id)).json, B.id), 'joined'); ok('the api records who joined');
  const active = (await http(B.token, 'GET', '/meetings')).json.meetings;
  assert.ok(active.some((x) => x.id === M.id && x.me === 'joined')); ok('an open meeting appears in my active meetings');
  const bad = await fetch(BASE + '/media/meetings/' + M.id + '/ws'); // plain HTTP GET, no upgrade
  assert.notEqual(bad.status, 200); ok('the signalling endpoint refuses plain HTTP');

  const wsBad = new WebSocket(BASE.replace('https', 'wss') + `/media/meetings/${M.id}/ws`);
  await new Promise((r) => { wsBad.onopen = r; });
  wsBad.send(JSON.stringify({ type: 'join', token: 'forged' }));
  const code = await new Promise((r) => { wsBad.onclose = (e) => r(e.code); });
  assert.equal(code, 1008); ok('a forged token is rejected by the media service');
}

// ---- more invitations, decline
{
  const r = await http(B.token, 'POST', `/meetings/${M.id}/invite`, { user_ids: [D.id] });
  assert.equal(r.status, 200, r.text);
  await wD.wait((e) => e.type === 'meeting_invite' && e.data.id === M.id); ok('a participant can invite more people');
  assert.equal((await meeting(D, M.id)).status, 200);
  assert.equal((await http(C.token, 'POST', `/meetings/${M.id}/decline`)).status, 204);
  assert.equal(statusOf((await meeting(A, M.id)).json, C.id), 'declined'); ok('declining is recorded');
  assert.ok(!(await http(C.token, 'GET', '/meetings')).json.meetings.some((x) => x.id === M.id)); ok('a declined meeting leaves my active list');
  assert.equal((await http(C.token, 'POST', `/meetings/${M.id}/decline`)).status, 409);
}

// ---- leaving: the media service tells the api when a connection closes
{
  mediaB.ws.close();
  await mediaA.until(() => mediaA.msgs.find((m) => m.type === 'peer_left' && m.user === B.id)); ok('the others are told when somebody leaves');
  const end = Date.now() + 5000; let st;
  while (Date.now() < end) { st = statusOf((await meeting(A, M.id)).json, B.id); if (st === 'left') break; await sleep(200); }
  assert.equal(st, 'left'); ok('the media service reports the departure to the api');
}

// ---- ending
{
  assert.equal((await http(B.token, 'POST', `/meetings/${M.id}/end`)).status, 403); ok('only the host can end a meeting for everybody');
  assert.equal((await http(A.token, 'POST', `/meetings/${M.id}/end`)).status, 204);
  await wD.wait((e) => e.type === 'meeting_ended' && e.data.id === M.id); ok('ending notifies everybody');
  await mediaA.until(() => mediaA.closed() !== null || mediaA.msgs.find((m) => m.type === 'ended')); ok('and closes the media room');
  assert.equal((await http(B.token, 'POST', `/meetings/${M.id}/token`)).status, 404); ok('an ended meeting cannot be joined');
  assert.ok((await http(B.token, 'GET', '/meetings?view=recent')).json.meetings.some((x) => x.id === M.id && x.ended_at)); ok('it moves to the recent meetings');
}

// ---- a meeting ends by itself when everybody has left
{
  const r = (await http(A.token, 'POST', '/meetings', { title: 'Courte', invitees: [B.id] })).json;
  const a = await joinMedia(A, r.id); const b = await joinMedia(B, r.id);
  a.ws.close(); b.ws.close();
  const end = Date.now() + 6000; let m;
  while (Date.now() < end) { m = (await meeting(A, r.id)).json; if (m.ended_at) break; await sleep(200); }
  assert.ok(m.ended_at); ok('a meeting closes itself once the last person has left');
}

// ---- one-to-one video call
{
  const r = await http(A.token, 'POST', '/meetings', { kind: 'video_call', invitees: [B.id] });
  assert.equal(r.status, 200); assert.equal(r.json.kind, 'video_call'); assert.equal(r.json.title, 'Appel vidéo');
  const inv = await wB.wait((e) => e.type === 'meeting_invite' && e.data.id === r.json.id && e.data.kind === 'video_call'); ok('a video call rings the other person');
  const a = await joinMedia(A, r.json.id);
  assert.equal((await http(B.token, 'POST', `/meetings/${r.json.id}/decline`)).status, 204);
  await wA.wait((e) => e.type === 'meeting_ended' && e.data.id === r.json.id && e.data.declined_by === B.id);
  assert.ok((await meeting(A, r.json.id)).json.ended_at); ok('refusing a video call ends it for the caller');
  assert.equal((await http(A.token, 'POST', `/meetings/${r.json.id}/invite`, { user_ids: [C.id] })).status, 404);
  a.ws.close();

  const r2 = (await http(A.token, 'POST', '/meetings', { kind: 'video_call', invitees: [B.id] })).json;
  assert.equal((await http(B.token, 'POST', `/meetings/${r2.id}/invite`, { user_ids: [C.id] })).status, 404); ok('a video call cannot be turned into a group by inviting');
  const ra = await joinMedia(A, r2.id); const rb = await joinMedia(B, r2.id);
  assert.equal(statusOf((await meeting(A, r2.id)).json, B.id), 'joined'); ok('answering a video call joins the same room');
  ra.ws.close(); rb.ws.close();
}

console.log(`\n${n} checks passed`);
process.exit(0);
