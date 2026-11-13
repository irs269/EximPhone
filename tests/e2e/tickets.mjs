// Ticket workflow acceptance test: file -> head approves/rejects -> head assigns -> member starts/resolves.
// Needs the dev stack with INITIAL_DEPT_HEADS (see .env.example) and the demo directory.
// Usage:  NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/tickets.mjs [https://localhost]
import assert from 'node:assert/strict';

const BASE = process.argv[2] ?? 'https://localhost';
const PASS = 'Demo-Passw0rd!';
let n = 0;
const ok = (name) => { n++; console.log(`PASS ${name}`); };

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
function connect(user) {
  const ws = new WebSocket(BASE.replace('https', 'wss') + '/api/ws');
  const events = [];
  const waiters = [];
  ws.onmessage = (m) => {
    const ev = JSON.parse(m.data); events.push(ev);
    for (const w of [...waiters]) if (w.match(ev)) { waiters.splice(waiters.indexOf(w), 1); w.resolve(ev); }
  };
  const wait = (match, ms = 3000) => new Promise((resolve, reject) => {
    const hit = events.find(match);
    if (hit) return resolve(hit);
    const w = { match, resolve }; waiters.push(w);
    setTimeout(() => { waiters.splice(waiters.indexOf(w), 1); reject(new Error('timeout waiting for event')); }, ms);
  });
  return new Promise((resolve, reject) => {
    ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token: user.token }));
    ws.onerror = () => reject(new Error('ws error'));
    wait((e) => e.type === 'ready').then(() => resolve({ ws, wait, events }), reject);
  });
}
const tev = (id, kind) => (e) => e.type === 'ticket' && e.data.id === id && e.data.kind === kind;

const admin = await login('asaid');                 // IT head and it_admin
const opsHead = await login('oabdallah'), finHead = await login('hbakari');   // heads of other services (all on the HQ site)
const req = await login('nyoussouf');               // Operations employee, files the tickets
const member = await login('kseif');                // IT employee (not a head)
const outsider = finHead;                           // unrelated to IT tickets
const wAdmin = await connect(admin), wReq = await connect(req), wMember = await connect(member);

const services = (await http(req.token, 'GET', '/tickets/services')).json.services;
const dept = (name) => services.find((s) => s.name === name);
assert.ok(dept('IT') && dept('Finance') && dept('Communication'));
assert.ok(dept('IT').heads.length >= 1); ok('services are listed with their heads');

// ---- happy path: IT ticket
let t;
{
  const bad = await http(req.token, 'POST', '/tickets', { title: '', description: 'x', department_id: dept('IT').id });
  assert.equal(bad.status, 400); ok('a ticket needs a title and a description');
  assert.equal((await http(req.token, 'POST', '/tickets', { title: 'x', description: 'y', department_id: 99999 })).status, 400); ok('unknown service is refused');
  assert.equal((await http(req.token, 'POST', '/tickets', { title: 'x', description: 'y', priority: 'asap', department_id: dept('IT').id })).status, 400); ok('invalid priority is refused');

  const r = await http(req.token, 'POST', '/tickets', { title: 'Poste de travail lent', description: 'Le PC met 10 minutes a demarrer.', priority: 'high', department_id: dept('IT').id });
  assert.equal(r.status, 200, r.text); t = r.json;
  assert.equal(t.status, 'pending');
  assert.ok(t.can.cancel && !t.can.decide); ok('filed ticket is pending; the requester can cancel but not decide');
  await wAdmin.wait(tev(t.id, 'created')); ok('the head of the service is notified in real time');

  assert.equal((await http(member.token, 'GET', `/tickets/${t.id}`)).status, 404); ok('an unrelated employee cannot see the ticket');
  assert.equal((await http(outsider.token, 'GET', `/tickets/${t.id}`)).status, 404);
  assert.equal((await http(member.token, 'POST', `/tickets/${t.id}/decision`, { approve: true })).status, 404);
  assert.equal((await http(req.token, 'POST', `/tickets/${t.id}/decision`, { approve: true })).status, 403); ok('the requester cannot approve their own ticket');
  assert.equal((await http(opsHead.token, 'POST', `/tickets/${t.id}/decision`, { approve: true })).status, 404); ok('the head of another service cannot decide it');

  const sum = (await http(admin.token, 'GET', '/tickets/summary')).json;
  assert.ok(sum.to_decide >= 1 && sum.waiting >= 1); ok('the head sees pending tickets in the summary badge');
  const svc = (await http(admin.token, 'GET', '/tickets?view=service')).json.tickets;
  assert.ok(svc.some((x) => x.id === t.id && x.can.decide)); ok('the head lists it among the tickets of the service with decide=true');

  assert.equal((await http(admin.token, 'POST', `/tickets/${t.id}/assign`, { user_id: member.id })).status, 403); ok('a pending ticket cannot be assigned before approval');
  assert.equal((await http(admin.token, 'POST', `/tickets/${t.id}/decision`, { approve: false })).status, 400); ok('rejecting requires a comment');
  const ap = await http(admin.token, 'POST', `/tickets/${t.id}/decision`, { approve: true, comment: 'Ok, on s en occupe.' });
  assert.equal(ap.status, 200, ap.text); assert.equal(ap.json.status, 'approved');
  await wReq.wait(tev(t.id, 'approved')); ok('head approves; the requester is notified');
  assert.equal((await http(admin.token, 'POST', `/tickets/${t.id}/decision`, { approve: false, comment: 'non' })).status, 403); ok('a ticket cannot be decided twice');
  assert.equal((await http(req.token, 'GET', `/tickets/${t.id}`)).json.can.cancel, true); ok('an approved ticket can still be cancelled by the requester');

  // assignment
  assert.equal((await http(admin.token, 'POST', `/tickets/${t.id}/assign`, { user_id: req.id })).status, 400); ok('the assignee must belong to the service (a requester from another service refused)');
  assert.equal((await http(member.token, 'POST', `/tickets/${t.id}/assign`, { user_id: member.id })).status, 404);
  const cands = (await http(admin.token, 'GET', `/tickets/${t.id}/candidates`)).json.candidates;
  assert.ok(cands.some((c) => c.id === member.id) && !cands.some((c) => c.id === req.id)); ok('candidates are the active members of the service');
  const as = await http(admin.token, 'POST', `/tickets/${t.id}/assign`, { user_id: member.id, comment: 'Pour toi.' });
  assert.equal(as.status, 200, as.text); assert.equal(as.json.status, 'assigned'); assert.equal(as.json.assignee_id, member.id);
  await wMember.wait(tev(t.id, 'assigned')); ok('head assigns to a member; the assignee is notified');
  assert.ok((await http(member.token, 'GET', `/tickets/${t.id}`)).json.can.start); ok('the assignee now sees the ticket and may start it');
  assert.ok((await http(member.token, 'GET', '/tickets?view=todo')).json.tickets.some((x) => x.id === t.id)); ok('it appears in the assignee to-do list');
  assert.ok((await http(member.token, 'GET', '/tickets/summary')).json.to_do >= 1);

  // execution
  assert.equal((await http(req.token, 'POST', `/tickets/${t.id}/start`)).status, 403); ok('only the assignee can start');
  assert.equal((await http(member.token, 'POST', `/tickets/${t.id}/resolve`, { comment: 'ok' })).status, 200); // resolve straight from assigned is allowed
  const done = (await http(req.token, 'GET', `/tickets/${t.id}`)).json;
  assert.equal(done.status, 'resolved'); assert.equal(done.resolution, 'ok');
  await wReq.wait(tev(t.id, 'resolved')); ok('assignee resolves; the requester is notified');
  assert.equal((await http(req.token, 'POST', `/tickets/${t.id}/cancel`)).status, 403); ok('a resolved ticket cannot be cancelled');
  assert.deepEqual(done.events.map((e) => e.kind), ['created', 'approved', 'assigned', 'resolved']); ok('history lists every step in order');
  assert.equal((await http(req.token, 'POST', `/tickets/${t.id}/comments`, { comment: 'Merci !' })).status, 200);
  assert.equal((await http(outsider.token, 'POST', `/tickets/${t.id}/comments`, { comment: 'hello' })).status, 404); ok('participants can comment, others cannot');
}

// ---- start then resolve, with re-assignment
{
  const r = await http(req.token, 'POST', '/tickets', { title: 'Imprimante', description: 'Bourrage permanent', department_id: dept('IT').id });
  const id = r.json.id;
  await http(admin.token, 'POST', `/tickets/${id}/decision`, { approve: true });
  await http(admin.token, 'POST', `/tickets/${id}/assign`, { user_id: member.id });
  assert.equal((await http(member.token, 'POST', `/tickets/${id}/start`)).json.status, 'in_progress');
  const other = (await http(admin.token, 'GET', `/tickets/${id}/candidates`)).json.candidates.find((c) => c.id !== member.id && c.id !== admin.id);
  const re = await http(admin.token, 'POST', `/tickets/${id}/assign`, { user_id: other.id });
  assert.equal(re.json.status, 'assigned'); assert.equal(re.json.assignee_id, other.id); ok('a head can re-assign a ticket in progress; it goes back to "assigned"');
  assert.equal((await http(member.token, 'POST', `/tickets/${id}/resolve`, { comment: 'x' })).status, 404); ok('the previous assignee loses access');
}

// ---- rejection
{
  const r = await http(req.token, 'POST', '/tickets', { title: 'Nouvel ecran 4K', description: 'Pour le confort', department_id: dept('Finance').id });
  const id = r.json.id;
  assert.equal((await http(finHead.token, 'POST', `/tickets/${id}/decision`, { approve: false, comment: 'Hors budget.' })).json.status, 'rejected');
  const got = (await http(req.token, 'GET', `/tickets/${id}`)).json;
  assert.equal(got.decision_comment, 'Hors budget.'); assert.ok(got.decided_by);
  assert.ok(!got.can.cancel && !got.can.decide);
  assert.equal((await http(finHead.token, 'POST', `/tickets/${id}/assign`, { user_id: outsider.id })).status, 403); ok('rejected ticket: reason kept, no assignment, no cancel');
}

// ---- cancellation by the requester
{
  const r = await http(req.token, 'POST', '/tickets', { title: 'Annule', description: 'Erreur', department_id: dept('IT').id });
  assert.equal((await http(req.token, 'POST', `/tickets/${r.json.id}/cancel`)).json.status, 'cancelled');
  assert.equal((await http(admin.token, 'POST', `/tickets/${r.json.id}/decision`, { approve: true })).status, 403); ok('the requester can cancel a pending ticket; it can no longer be decided');
}

// ---- separation of duties: a head cannot approve their own ticket
{
  const r = await http(admin.token, 'POST', '/tickets', { title: 'Acces VPN', description: 'Pour le teletravail', department_id: dept('IT').id });
  assert.equal(r.status, 200); assert.equal(r.json.can.decide, false);
  assert.equal((await http(admin.token, 'POST', `/tickets/${r.json.id}/decision`, { approve: true })).status, 403); ok('a head (even it_admin) cannot approve their own ticket');
  await http(admin.token, 'POST', `/tickets/${r.json.id}/cancel`);
}

// ---- heads administration
{
  assert.equal((await http(req.token, 'GET', '/admin/departments')).status, 403);
  assert.equal((await http(req.token, 'PUT', `/admin/departments/${dept('Communication').id}/heads`, { user_ids: [] })).status, 403); ok('only it_admin manages the heads of service');
  const zali = (await login('imoussa')).id;
  assert.equal((await http(admin.token, 'PUT', `/admin/departments/${dept('Communication').id}/heads`, { user_ids: [] })).status, 200);
  const none = await http(req.token, 'POST', '/tickets', { title: 'x', description: 'y', department_id: dept('Communication').id });
  assert.equal(none.status, 409); ok('a service without head cannot receive tickets');
  assert.equal((await http(admin.token, 'PUT', `/admin/departments/${dept('Communication').id}/heads`, { user_ids: [999999] })).status, 400);
  assert.equal((await http(admin.token, 'PUT', `/admin/departments/${dept('Communication').id}/heads`, { user_ids: [zali] })).status, 200); ok('heads can be replaced (and restored)');
  const deps = (await http(admin.token, 'GET', '/admin/departments')).json.departments;
  assert.ok(deps.find((d) => d.name === 'Communication').heads.some((h) => h.id === zali));
}

// ---- my tickets
{
  const mine = (await http(req.token, 'GET', '/tickets?view=mine')).json.tickets;
  assert.ok(mine.length >= 4 && mine.every((x) => x.requester_id === req.id)); ok('"my tickets" lists only the tickets I filed');
}

console.log(`\n${n} checks passed`);
for (const w of [wAdmin, wReq, wMember]) w.ws.close();
process.exit(0);
