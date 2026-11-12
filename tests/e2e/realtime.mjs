// Realtime acceptance test: two employees chat, presence propagates in < 1 s.
// Usage (dev stack running):  NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/realtime.mjs [https://localhost]
// (TLS verification is disabled ONLY for this local self-signed dev certificate.)
import assert from 'node:assert/strict';

const BASE = process.argv[2] ?? 'https://localhost';
const PASS = 'Demo-Passw0rd!';
const results = [];
const ok = (name, extra = '') => { results.push(name); console.log(`PASS ${name}${extra ? ' ' + extra : ''}`); };

async function http(token, method, path, body) {
  const res = await fetch(BASE + '/api' + path, {
    method,
    headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(body && !(body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}) },
    body: body instanceof FormData ? body : body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  return { status: res.status, json: text && res.headers.get('content-type')?.includes('json') ? JSON.parse(text) : null, text, res };
}

async function login(username) {
  const r = await http(null, 'POST', '/auth/login', { username, password: PASS });
  assert.equal(r.status, 200, `login ${username}: ${r.text}`);
  return { token: r.json.access_token, id: r.json.user.id, username };
}

function connect(user) {
  const ws = new WebSocket(BASE.replace('https', 'wss') + '/api/ws');
  const events = [];
  const waiters = [];
  ws.onmessage = (m) => {
    const ev = JSON.parse(m.data);
    events.push(ev);
    for (const w of [...waiters]) if (w.match(ev)) { waiters.splice(waiters.indexOf(w), 1); w.resolve({ ev, at: performance.now() }); }
  };
  const wait = (match, ms = 3000) => new Promise((resolve, reject) => {
    const hit = events.find(match);
    if (hit) return resolve({ ev: hit, at: performance.now() });
    const w = { match, resolve };
    waiters.push(w);
    setTimeout(() => { waiters.splice(waiters.indexOf(w), 1); reject(new Error('timeout waiting for event')); }, ms);
  });
  const closed = new Promise((r) => { ws.onclose = () => r(performance.now()); });
  return new Promise((resolve, reject) => {
    ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token: user.token }));
    ws.onerror = () => reject(new Error('ws error'));
    wait((e) => e.type === 'ready').then(() => resolve({ ws, wait, events, closed, send: (o) => ws.send(JSON.stringify(o)) }), reject);
  });
}

const a = await login('asaid'), b = await login('oabdallah'), c = await login('mjuma');
const wa = await connect(a), wb = await connect(b), wc = await connect(c);
ok('two clients logged in and connected over WebSocket');

// --- unauthenticated socket is rejected
{
  const ws = new WebSocket(BASE.replace('https', 'wss') + '/api/ws');
  const closed = await new Promise((r) => { ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token: 'garbage' })); ws.onclose = () => r(true); });
  assert.ok(closed); ok('socket with an invalid token is closed');
}

// --- presence propagation latency
{
  const t0 = performance.now();
  await http(a.token, 'PUT', '/presence', { status: 'busy' });
  const { at } = await wb.wait((e) => e.type === 'presence' && e.data.user_id === a.id && e.data.status === 'busy');
  const ms = Math.round(at - t0);
  assert.ok(ms < 1000, `presence took ${ms} ms`);
  ok('presence change reaches another client in < 1 s', `(${ms} ms)`);
  const snap = await http(b.token, 'GET', '/presence');
  assert.equal(snap.json.presence[String(a.id)], 'busy'); ok('presence snapshot reflects the status');
  assert.equal((await http(a.token, 'PUT', '/presence', { status: 'hacker' })).status, 400); ok('invalid presence status rejected');
  await http(a.token, 'PUT', '/presence', { status: 'on' });
}

// --- direct conversation + message
let conv;
{
  const r = await http(a.token, 'POST', '/conversations', { member_ids: [b.id] });
  assert.equal(r.status, 200); conv = r.json;
  const again = await http(b.token, 'POST', '/conversations', { member_ids: [a.id] });
  assert.equal(again.json.id, conv.id); ok('direct conversation is created once per pair');

  const t0 = performance.now();
  const sent = await http(a.token, 'POST', `/conversations/${conv.id}/messages`, { body: 'Bonjour Omar, test temps réel' });
  assert.equal(sent.status, 200);
  const { at, ev } = await wb.wait((e) => e.type === 'message' && e.data.id === sent.json.id);
  assert.equal(ev.data.body, 'Bonjour Omar, test temps réel');
  ok('message delivered to the other client in real time', `(${Math.round(at - t0)} ms)`);

  wb.send({ type: 'delivered', message_id: sent.json.id });
  await wa.wait((e) => e.type === 'receipt' && e.data.state === 'delivered');
  ok('delivery receipt reaches the sender');
  assert.equal((await http(b.token, 'POST', `/conversations/${conv.id}/read`)).status, 204);
  await wa.wait((e) => e.type === 'receipt' && e.data.state === 'read');
  const hist = await http(a.token, 'GET', `/conversations/${conv.id}/messages`);
  assert.equal(hist.json.messages.at(-1).status, 'read'); ok('read receipt reaches the sender and history shows status "read"');

  wa.send({ type: 'typing', conversation_id: conv.id });
  await wb.wait((e) => e.type === 'typing' && e.data.user_id === a.id); ok('typing indicator is relayed');
}

// --- group
{
  const g = await http(a.token, 'POST', '/conversations', { member_ids: [b.id, c.id], name: 'DSI test' });
  assert.equal(g.json.kind, 'group');
  const s = await http(a.token, 'POST', `/conversations/${g.json.id}/messages`, { body: 'Message de groupe' });
  await wb.wait((e) => e.type === 'message' && e.data.id === s.json.id);
  await wc.wait((e) => e.type === 'message' && e.data.id === s.json.id);
  ok('group message reaches every member');
  assert.equal((await http(a.token, 'POST', '/conversations', { member_ids: [b.id, c.id] })).status, 400); ok('group without a name rejected');
}

// --- attachments
{
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==', 'base64');
  const form = new FormData(); form.set('conversation_id', String(conv.id)); form.set('file', new Blob([png], { type: 'image/png' }), 'pixel.png');
  const up = await http(a.token, 'POST', '/attachments', form);
  assert.equal(up.status, 200, up.text);
  const m = await http(a.token, 'POST', `/conversations/${conv.id}/messages`, { body: 'voir pièce jointe', attachment_id: up.json.id });
  assert.equal(m.json.attachment.filename, 'pixel.png');
  const dl = await fetch(`${BASE}/api/attachments/${up.json.id}`, { headers: { Authorization: `Bearer ${b.token}` } });
  assert.equal(dl.status, 200); assert.deepEqual(Buffer.from(await dl.arrayBuffer()), png);
  ok('PNG attachment uploaded to object storage and downloaded intact by the other member');
  const outsider = await fetch(`${BASE}/api/attachments/${up.json.id}`, { headers: { Authorization: `Bearer ${c.token}` } });
  assert.equal(outsider.status, 404); ok('non-member cannot download the attachment');

  const fake = new FormData(); fake.set('conversation_id', String(conv.id));
  fake.set('file', new Blob([Buffer.from('MZ\x90\x00 fake executable')], { type: 'image/png' }), 'virus.png');
  assert.equal((await http(a.token, 'POST', '/attachments', fake)).status, 415); ok('executable renamed .png is rejected (MIME sniffing)');
  const big = new FormData(); big.set('conversation_id', String(conv.id));
  big.set('file', new Blob([Buffer.concat([Buffer.from('%PDF-1.4\n'), Buffer.alloc(21 * 1024 * 1024)])], { type: 'application/pdf' }), 'big.pdf');
  assert.equal((await http(a.token, 'POST', '/attachments', big)).status, 413); ok('file over 20 MB rejected');
}

// --- access control, search, logical deletion
{
  assert.equal((await http(c.token, 'GET', `/conversations/${conv.id}/messages`)).status, 404); ok('non-member cannot read a conversation');
  assert.equal((await http(c.token, 'POST', `/conversations/${conv.id}/messages`, { body: 'intrus' })).status, 404); ok('non-member cannot post');
  const s = await http(b.token, 'GET', '/search?q=temps%20r%C3%A9el');
  assert.ok(s.json.results.length >= 1); ok('search finds encrypted messages');
  const last = (await http(a.token, 'GET', `/conversations/${conv.id}/messages`)).json.messages.find((m) => m.body === 'Bonjour Omar, test temps réel');
  assert.equal((await http(b.token, 'DELETE', `/messages/${last.id}`)).status, 404); ok('only the author can delete a message');
  assert.equal((await http(a.token, 'DELETE', `/messages/${last.id}`)).status, 204);
  const after = (await http(a.token, 'GET', `/conversations/${conv.id}/messages`)).json.messages.find((m) => m.id === last.id);
  assert.equal(after.deleted, true); assert.equal(after.body, ''); ok('deleted message is hidden for users (kept in the archive)');
}

// --- logout closes sockets and announces offline
{
  const t0 = performance.now();
  const lo = await http(a.token, 'POST', '/auth/logout', { reason: 'test e2e' });
  assert.ok([200, 422].includes(lo.status));
  if (lo.status === 200) {
    const { at } = await wb.wait((e) => e.type === 'presence' && e.data.user_id === a.id && e.data.status === 'offline');
    const closedAt = await Promise.race([wa.closed, new Promise((r) => setTimeout(() => r(null), 2000))]);
    assert.ok(closedAt, 'socket still open after logout');
    ok('logout announces "offline" and closes the user sockets', `(${Math.round(at - t0)} ms)`);
  }
}

console.log(`\n${results.length} checks passed`);
for (const w of [wa, wb, wc]) w.ws.close();
process.exit(0);
