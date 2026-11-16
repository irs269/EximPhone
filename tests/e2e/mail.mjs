// Internal mail acceptance test: send, cc/bcc, threads, folders, drafts, attachments, forward, search, privacy.
// Usage:  NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/mail.mjs [https://localhost]
import assert from 'node:assert/strict';

const BASE = process.argv[2] ?? 'https://localhost';
const PASS = 'Demo-Passw0rd!';
const RUN = Date.now().toString(36); // makes subjects unique across runs
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
  const events = []; const waiters = [];
  ws.onmessage = (m) => {
    const ev = JSON.parse(m.data); events.push(ev);
    for (const w of [...waiters]) if (w.match(ev)) { waiters.splice(waiters.indexOf(w), 1); w.resolve(ev); }
  };
  const wait = (match, ms = 3000) => new Promise((resolve, reject) => {
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
async function upload(user, name, content) {
  const fd = new FormData();
  fd.append('file', new Blob([content]), name);
  const res = await fetch(BASE + '/api/mail/attachments', { method: 'POST', headers: { Authorization: `Bearer ${user.token}` }, body: fd });
  return { status: res.status, json: await res.json().catch(() => null) };
}
async function download(user, id) {
  const res = await fetch(BASE + `/api/mail/attachments/${id}`, { headers: { Authorization: `Bearer ${user.token}` } });
  return { status: res.status, buf: Buffer.from(await res.arrayBuffer()), headers: res.headers };
}
const list = async (u, folder, q = '', page = 0) => (await http(u.token, 'GET', `/mail/messages?folder=${folder}&q=${encodeURIComponent(q)}&page=${page}`)).json;
const folders = async (u) => (await http(u.token, 'GET', '/mail/folders')).json;
const thread = async (u, id, folder = '') => http(u.token, 'GET', `/mail/threads/${id}${folder ? `?folder=${folder}` : ''}`);
const find = (res, subject) => res.items.find((i) => i.subject === subject);
const inThread = (res, t) => res.items.some((i) => i.thread_id === t);

const A = await login('nyoussouf'), B = await login('oabdallah'), C = await login('imoussa'), D = await login('kseif'), E = await login('asaid');
const wB = await connect(B);

// ---- send with to / cc / bcc
const S1 = `Budget Q4 ${RUN} é`;
let m1, thread1;
{
  const unreadBefore = (await folders(B)).unread_inbox;
  const r = await http(A.token, 'POST', '/mail/send', { to: [B.id], cc: [C.id], bcc: [D.id], subject: S1, body: 'Bonjour,\nVoici le budget du trimestre.\nCordialement' });
  assert.equal(r.status, 200, r.text); m1 = r.json.id; thread1 = r.json.thread_id;
  assert.equal(thread1, m1); ok('a message is sent and starts its own conversation');
  const ev = await wB.wait((e) => e.type === 'mail' && e.data.id === m1);
  assert.equal(ev.data.from_id, A.id); ok('the recipient is notified in real time');
  for (const u of [B, C, D]) assert.ok(find(await list(u, 'inbox'), S1), `${u.name} inbox`);
  ok('to, cc and bcc recipients all receive it in their inbox');
  const sent = find(await list(A, 'sent'), S1);
  assert.ok(sent && sent.to.includes('Omar Abdallah') && sent.to.length === 3); ok('the sender sees it in Sent with all recipients (including bcc)');
  assert.ok(!find(await list(A, 'inbox'), S1)); ok('the sender does not receive their own message in the inbox');
  assert.equal((await folders(B)).unread_inbox, unreadBefore + 1); ok('the unread counter goes up');
  const item = find(await list(B, 'inbox'), S1);
  assert.ok(item.unread && item.snippet.startsWith('Bonjour,') && item.from.name); ok('the list shows sender, subject, snippet and unread state');
}

// ---- privacy of bcc and reading
{
  const tB = (await thread(B, thread1)).json;
  assert.equal(tB.messages.length, 1);
  const msg = tB.messages[0];
  assert.equal(msg.subject, S1); assert.ok(msg.body.includes('budget du trimestre'));
  assert.deepEqual(msg.to.map((p) => p.id), [B.id]); assert.deepEqual(msg.cc.map((p) => p.id), [C.id]); assert.equal(msg.bcc.length, 0);
  ok('the recipients never see the bcc, and subject and body come back intact (accents, line breaks)');
  const tA = (await thread(A, thread1)).json.messages[0];
  assert.deepEqual(tA.bcc.map((p) => p.id), [D.id]); assert.ok(tA.mine); ok('the sender sees who was in bcc');
  assert.equal((await thread(E, thread1)).status, 404); ok('someone who is not a participant cannot read the conversation');
  const before = (await folders(B)).unread_inbox;
  assert.equal((await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'read' })).json.updated, 1);
  assert.equal((await folders(B)).unread_inbox, before - 1); ok('marking as read lowers the unread counter');
  assert.equal((await http(E.token, 'POST', '/mail/batch', { ids: [m1], action: 'trash' })).json.updated, 0); ok('actions only touch the caller\'s own mailbox');
  await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'unread' });
  assert.ok(find(await list(B, 'inbox'), S1).unread); ok('a message can be marked unread again');
}

// ---- reply: same conversation
let m2;
{
  const r = await http(B.token, 'POST', '/mail/send', { to: [A.id], cc: [C.id], subject: `Re: ${S1}`, body: 'Merci, validé.', parent_id: m1 });
  assert.equal(r.status, 200, r.text); m2 = r.json.id; assert.equal(r.json.thread_id, thread1); ok('a reply joins the conversation');
  const tA = (await thread(A, thread1)).json;
  assert.deepEqual(tA.messages.map((m) => m.id), [m1, m2]); ok('the conversation shows both messages in order');
  const inbox = await list(A, 'inbox');
  const it = inbox.items.find((i) => i.thread_id === thread1);
  assert.ok(it && it.unread && it.subject === `Re: ${S1}`); ok('the sender of the original sees the reply as the latest unread message');
  const sentB = (await list(B, 'inbox')).items.filter((i) => i.thread_id === thread1);
  assert.equal(sentB.length, 1); assert.equal(sentB[0].count, 1); // B: only the original in the inbox, the reply is in B\'s Sent
  assert.ok((await list(B, 'sent')).items.some((i) => i.thread_id === thread1)); ok('conversations are grouped per folder');
  assert.equal((await http(E.token, 'POST', '/mail/send', { to: [A.id], subject: 'x', body: 'y', parent_id: m1 })).status, 404); ok('one cannot reply into a conversation one is not part of');
}

// ---- star, archive, trash, restore, purge
{
  await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'star' });
  assert.ok(find(await list(B, 'starred'), S1)); assert.ok(find(await list(B, 'inbox'), S1).starred); ok('starring puts the conversation in Starred');
  await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'unstar' });
  assert.ok(!find(await list(B, 'starred'), S1)); ok('un-starring removes it');
  await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'archive' });
  assert.ok(!find(await list(B, 'inbox'), S1) && find(await list(B, 'archive'), S1)); ok('archiving moves it out of the inbox');
  await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'trash' });
  assert.ok(find(await list(B, 'trash'), S1) && !find(await list(B, 'archive'), S1)); ok('deleting moves it to the trash');
  assert.ok(find(await list(A, 'sent'), S1) && inThread(await list(C, 'inbox'), thread1)); ok('another person\'s copies are not affected');
  assert.equal((await thread(B, thread1, 'trash')).status, 200); ok('a trashed message can still be opened from the trash');
  await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'restore' });
  assert.ok(find(await list(B, 'archive'), S1)); ok('restoring puts it back where it came from');
  assert.equal((await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'purge' })).json.updated, 0); ok('"delete forever" only works from the trash');
  await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'trash' });
  assert.equal((await http(B.token, 'POST', '/mail/batch', { ids: [m1], action: 'purge' })).json.updated, 1);
  assert.ok(!find(await list(B, 'trash'), S1) && !find(await list(B, 'archive'), S1)); ok('delete forever hides it for that user');
  assert.ok(find(await list(A, 'sent'), S1)); ok('...but the other participants keep their copy');
}

// ---- drafts
{
  const S = `Brouillon ${RUN}`;
  const before = (await folders(A)).drafts;
  const d = await http(A.token, 'POST', '/mail/drafts', { subject: S, body: 'a compléter', to: [] });
  assert.equal(d.status, 200, d.text); const id = d.json.id;
  assert.equal((await folders(A)).drafts, before + 1); assert.ok(find(await list(A, 'drafts'), S).draft); ok('a draft can be saved without recipients');
  assert.equal((await http(A.token, 'POST', `/mail/drafts/${id}/send`)).status, 400); ok('sending a draft needs a recipient');
  const up = await http(A.token, 'POST', '/mail/drafts', { id, subject: S + ' v2', body: 'complété', to: [C.id] });
  assert.equal(up.json.id, id); assert.ok(find(await list(A, 'drafts'), S + ' v2')); ok('a draft can be updated');
  assert.equal((await http(B.token, 'POST', '/mail/drafts', { id, subject: 'hack', body: '', to: [B.id] })).status, 404);
  assert.equal((await http(B.token, 'POST', `/mail/drafts/${id}/send`)).status, 404); ok('nobody else can edit or send my draft');
  assert.equal((await http(A.token, 'POST', `/mail/drafts/${id}/send`)).status, 200);
  assert.ok(!find(await list(A, 'drafts'), S + ' v2') && find(await list(C, 'inbox'), S + ' v2')); ok('sending a draft delivers it and removes it from Drafts');
  assert.equal((await http(A.token, 'POST', `/mail/drafts/${id}/send`)).status, 404); ok('a message cannot be sent twice');
  const d2 = await http(A.token, 'POST', '/mail/drafts', { subject: 'poubelle', body: 'x' });
  assert.equal((await http(A.token, 'DELETE', `/mail/drafts/${d2.json.id}`)).status, 204);
  assert.equal((await http(A.token, 'DELETE', `/mail/drafts/${id}`)).status, 404); ok('an unsent draft can be discarded; a sent message cannot');
}

// ---- attachments and forwarding
{
  const S = `Contrat ${RUN}`;
  const content = 'CONTRAT\nligne 2 — accentuée\n'.repeat(50);
  const up = await upload(A, 'contrat.txt', content);
  assert.equal(up.status, 200, JSON.stringify(up)); assert.equal(up.json.filename, 'contrat.txt');
  assert.equal((await upload(A, 'virus.exe', 'MZ')).status, 415); ok('executables and scripts are refused');
  const own = await download(A, up.json.id);
  assert.equal(own.status, 200); assert.equal((await download(B, up.json.id)).status, 404); ok('an unsent upload is only visible to its uploader');
  const r = await http(A.token, 'POST', '/mail/send', { to: [B.id], subject: S, body: 'voir pièce jointe', attachment_ids: [up.json.id] });
  assert.equal(r.status, 200, r.text);
  const msg = (await thread(B, r.json.thread_id)).json.messages[0];
  assert.equal(msg.attachments.length, 1); assert.equal(msg.attachments[0].filename, 'contrat.txt');
  assert.ok(find(await list(B, 'inbox'), S).has_attachments); ok('the recipient sees the attachment');
  const dl = await download(B, msg.attachments[0].id);
  assert.equal(dl.buf.toString('utf8'), content); assert.match(dl.headers.get('x-content-type-options'), /nosniff/);
  assert.match(dl.headers.get('content-disposition'), /attachment/); ok('the attachment is delivered intact, as a download');
  assert.equal((await download(E, msg.attachments[0].id)).status, 404); ok('a non-participant cannot download it');
  assert.equal((await http(A.token, 'POST', '/mail/send', { to: [B.id], subject: 'x', body: 'y', attachment_ids: [up.json.id] })).status, 400); ok('an attachment cannot be reused by another message');

  // forward: B forwards to E, attachment comes along
  const f = await http(B.token, 'POST', '/mail/send', { to: [E.id], subject: `Fwd: ${S}`, body: '---- message transféré ----', forward_of: msg.id });
  assert.equal(f.status, 200, f.text);
  const fm = (await thread(E, f.json.thread_id)).json.messages[0];
  assert.equal(fm.attachments.length, 1);
  assert.equal((await download(E, fm.attachments[0].id)).buf.toString('utf8'), content); ok('a forwarded message carries its attachments');
  assert.equal((await http(D.token, 'POST', '/mail/drafts', { subject: 'x', body: 'y', forward_of: msg.id })).status, 404); ok('one cannot forward a message one cannot see');
}

// ---- search
{
  assert.ok(inThread(await list(C, 'inbox', 'budget du trimestre'), thread1)); ok('search finds a word in the body');
  assert.ok(inThread(await list(C, 'inbox', S1.toLowerCase()), thread1)); ok('search finds the subject, case-insensitively');
  assert.ok(find(await list(A, 'sent', 'Omar Abdallah'), S1)); ok('search finds a recipient by name');
  assert.equal((await list(C, 'inbox', 'mot-introuvable-xyz')).items.length, 0); ok('search returns nothing for an unknown word');
  assert.ok(!inThread(await list(E, 'inbox', 'budget du trimestre'), thread1)); ok('search never reaches other people\'s mail');
}

// ---- self mail, validation, pagination
{
  const S = `Pense-bête ${RUN}`;
  assert.equal((await http(A.token, 'POST', '/mail/send', { to: [A.id], subject: S, body: 'rappel' })).status, 200);
  assert.ok(find(await list(A, 'inbox'), S) && find(await list(A, 'sent'), S)); ok('mailing oneself shows in both Inbox and Sent');
  assert.equal((await http(A.token, 'POST', '/mail/send', { to: [999999], subject: 'x', body: 'y' })).status, 400); ok('an unknown recipient is refused');
  assert.equal((await http(A.token, 'POST', '/mail/send', { to: [], subject: 'x', body: 'y' })).status, 400); ok('a message needs at least one recipient');
  const many = Array.from({ length: 51 }, (_, i) => 100000 + i);
  assert.equal((await http(A.token, 'POST', '/mail/send', { to: many, subject: 'x', body: 'y' })).status, 400); ok('too many recipients are refused');
  assert.equal((await http(A.token, 'GET', '/mail/messages?folder=nope')).status, 400); ok('an unknown folder is refused');
  for (let i = 0; i < 32; i++) await http(A.token, 'POST', '/mail/send', { to: [D.id], subject: `Lot ${RUN} ${i}`, body: 'x' });
  const p0 = await list(D, 'inbox'), p1 = await list(D, 'inbox', '', 1);
  assert.equal(p0.items.length, 30); assert.equal(p0.more, true); assert.ok(p1.items.length >= 3);
  assert.ok(!p1.items.some((i) => p0.items.some((j) => j.thread_id === i.thread_id))); ok('long folders are paginated without duplicates');
}

console.log(`\n${n} checks passed`);
wB.ws.close();
process.exit(0);
