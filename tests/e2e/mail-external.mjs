// External mail acceptance test against the GreenMail test server of the dev stack (SMTP 3025, IMAP 3143 on localhost).
// Outgoing: the platform relays to GreenMail; incoming: a message is injected over SMTP into the relay mailbox the platform polls.
// Usage:  NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/mail-external.mjs [https://localhost]
import assert from 'node:assert/strict';
import net from 'node:net';

const BASE = process.argv[2] ?? 'https://localhost';
const PASS = 'Demo-Passw0rd!';
const RUN = Date.now().toString(36);
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
async function upload(user, name, content) {
  const fd = new FormData(); fd.append('file', new Blob([content]), name);
  const res = await fetch(BASE + '/api/mail/attachments', { method: 'POST', headers: { Authorization: `Bearer ${user.token}` }, body: fd });
  return { status: res.status, json: await res.json().catch(() => null) };
}
async function download(user, id) {
  const res = await fetch(BASE + `/api/mail/attachments/${id}`, { headers: { Authorization: `Bearer ${user.token}` } });
  return { status: res.status, buf: Buffer.from(await res.arrayBuffer()) };
}
const list = async (u, folder, q = '') => (await http(u.token, 'GET', `/mail/messages?folder=${folder}&q=${encodeURIComponent(q)}`)).json;
const thread = async (u, id) => (await http(u.token, 'GET', `/mail/threads/${id}`)).json;

// ---- a minimal SMTP client: injects a message as if it came from the Internet ----
function smtpInject(rcpt, data) {
  return new Promise((resolve, reject) => {
    const s = net.connect(3025, '127.0.0.1');
    const steps = ['EHLO partner.test', 'MAIL FROM:<jean@partner.test>', `RCPT TO:<${rcpt}>`, 'DATA'];
    let buf = '', i = -1, state = 'banner';
    s.on('data', (d) => {
      buf += d.toString();
      if (!buf.endsWith('\r\n')) return;
      const lines = buf.trim().split('\r\n'); const last = lines[lines.length - 1];
      buf = '';
      if (/^\d{3}-/.test(last)) return; // multi-line reply continues
      const code = Number(last.slice(0, 3));
      if (code >= 400) { s.destroy(); return reject(new Error('SMTP: ' + last)); }
      if (state === 'data-sent') { s.write('QUIT\r\n'); s.end(); return resolve(); }
      if (state === 'body') { state = 'data-sent'; s.write(data.replace(/\n/g, '\r\n').replace(/\r\r\n/g, '\r\n') + '\r\n.\r\n'); return; }
      i++;
      if (i < steps.length) { if (steps[i] === 'DATA') state = 'body'; s.write(steps[i] + '\r\n'); }
    });
    s.on('error', reject);
  });
}

// ---- a minimal IMAP client: lists the raw messages of a mailbox ----
function imapMessages(user, pass) {
  return new Promise((resolve) => {
    const s = net.connect(3143, '127.0.0.1');
    let buf = Buffer.alloc(0); let waiting = null;
    const check = () => {
      if (!waiting) return;
      const text = buf.toString('latin1');
      const m = new RegExp(`(^|\r\n)${waiting.tag} (OK|NO|BAD)[^\r\n]*\r\n`).exec(text);
      if (!m) return;
      const out = buf; buf = Buffer.alloc(0); const cb = waiting.cb; waiting = null; cb(out);
    };
    s.on('data', (d) => { buf = Buffer.concat([buf, d]); check(); });
    s.on('error', () => resolve([]));
    const run = (tag, cmd) => new Promise((r) => { waiting = { tag, cb: r }; s.write(`${tag} ${cmd}\r\n`); check(); });
    setTimeout(async () => {
      buf = Buffer.alloc(0); // greeting
      await run('a1', `LOGIN "${user}" "${pass}"`);
      await run('a2', 'SELECT INBOX');
      const r = await run('a3', 'FETCH 1:* (BODY.PEEK[])');
      const out = []; const text = r.toString('latin1'); const re = /\{(\d+)\}\r\n/g; let m;
      while ((m = re.exec(text))) {
        const start = m.index + m[0].length; const len = Number(m[1]);
        out.push(r.subarray(start, start + len).toString('utf8'));
        re.lastIndex = start + len;
      }
      await run('a4', 'LOGOUT');
      s.end(); resolve(out);
    }, 300);
    setTimeout(() => { s.destroy(); resolve([]); }, 10000);
  });
}
async function waitFor(fn, what, ms = 40000) {
  const end = Date.now() + ms;
  for (;;) {
    const v = await fn();
    if (v) return v;
    if (Date.now() > end) throw new Error('timeout waiting for ' + what);
    await sleep(1500);
  }
}
const qpDecode = (s) => s.replace(/=\r?\n/g, '').replace(/=([0-9A-F]{2})/gi, (_, h) => String.fromCharCode(parseInt(h, 16)));
const qpUtf8 = (s) => Buffer.from(qpDecode(s), 'latin1').toString('utf8');

const A = await login('nyoussouf'), B = await login('oabdallah');

// ---- configuration is visible to the UI
{
  const f = (await http(A.token, 'GET', '/mail/folders')).json;
  assert.equal(f.external, true); ok('the server reports that external mail is configured');
}

// ---- outgoing
const OUT = `Offre ${RUN} é`;
let outThread;
{
  const up = await upload(A, 'offre.txt', 'Offre commerciale\nligne accentuée é\n'.repeat(20));
  assert.equal(up.status, 200);
  const r = await http(A.token, 'POST', '/mail/send', {
    to: [B.id], to_ext: ['Jean Dupont <jean@partner.test>'], cc_ext: ['cc@partner.test'], bcc_ext: ['secret@partner.test'],
    subject: OUT, body: 'Bonjour,\nVeuillez trouver notre offre, présentée ci-jointe.\nCordialement', attachment_ids: [up.json.id],
  });
  assert.equal(r.status, 200, r.text); outThread = r.json.thread_id;
  ok('a message with internal and external recipients is accepted');
  assert.ok((await list(B, 'inbox')).items.some((i) => i.thread_id === outThread)); ok('the internal recipient receives it immediately');
  const sent = (await list(A, 'sent')).items.find((i) => i.thread_id === outThread);
  assert.ok(sent.to.some((x) => x === 'Jean Dupont') && sent.to.includes('cc@partner.test')); ok('the sender sees the external recipients in Sent');

  const msgs = await waitFor(async () => { const m = await imapMessages('jean@partner.test', 'x'); return m.some((x) => x.includes(RUN)) ? m : null; }, 'the relayed message in the external mailbox');
  const raw = msgs.find((m) => m.includes(RUN));
  assert.match(raw, /^From: .*nyoussouf@exim\.local/mi); ok('the relayed message is sent from the user\'s own address');
  assert.match(raw, /^To: .*jean@partner\.test/mi); assert.match(raw, /^To: .*oabdallah@exim\.local/mi); assert.match(raw, /^Cc: .*cc@partner\.test/mi); ok('To and Cc list every recipient, internal ones by e-mail address');
  assert.ok(!/secret@partner\.test/i.test(raw.split(/\r?\n\r?\n/)[0])); ok('Bcc recipients are not in the headers');
  const secret = await waitFor(async () => { const m = await imapMessages('secret@partner.test', 'x'); return m.some((x) => x.includes(RUN)) ? m : null; }, 'the Bcc copy');
  assert.ok(secret.length >= 1); ok('...but the Bcc recipient still receives the message');
  assert.match(raw, /^Subject: =\?utf-8\?q\?/mi); assert.match(raw, /^Message-ID: <[0-9a-f]+@exim\.local>/mi);
  assert.match(raw, /multipart\/mixed/); assert.match(raw, /filename="?offre\.txt"?/); ok('it is a well-formed MIME message with the attachment');
  assert.ok(qpUtf8(raw).includes('Veuillez trouver notre offre, présentée ci-jointe.')); ok('the body is delivered intact (UTF-8, quoted-printable)');
}

// ---- an address of a platform user is delivered internally
{
  const S = `Interne par adresse ${RUN}`;
  const r = await http(A.token, 'POST', '/mail/send', { to_ext: ['oabdallah@exim.local'], subject: S, body: 'x' });
  assert.equal(r.status, 200, r.text);
  assert.ok((await list(B, 'inbox')).items.some((i) => i.subject === S)); ok('typing a colleague\'s e-mail address delivers internally (no relay)');
  assert.equal((await http(A.token, 'POST', '/mail/send', { to_ext: ['pas une adresse'], subject: 'x', body: 'y' })).status, 400); ok('an invalid e-mail address is refused');
}

// ---- incoming
const IN = `Demande de devis ${RUN} é`;
let inThread, inMsg;
{
  const b64 = Buffer.from('Devis n°42\nTotal: 1000\n').toString('base64');
  const subjectEnc = '=?UTF-8?B?' + Buffer.from(IN).toString('base64') + '?=';
  const mail = [
    'From: Jean Dupont <jean@partner.test>', 'To: nyoussouf@exim.local', 'Cc: oabdallah@exim.local, tiers@partner.test',
    `Subject: ${subjectEnc}`, `Message-ID: <in-${RUN}@partner.test>`, 'Date: Mon, 16 Nov 2026 10:00:00 +0000', 'MIME-Version: 1.0',
    'Content-Type: multipart/mixed; boundary="BND"', '', '--BND', 'Content-Type: text/plain; charset=utf-8', 'Content-Transfer-Encoding: base64', '',
    Buffer.from('Bonjour,\nPouvez-vous nous envoyer un devis ?\nMerci').toString('base64'), '--BND',
    'Content-Type: application/octet-stream; name="devis.txt"', 'Content-Disposition: attachment; filename="devis.txt"', 'Content-Transfer-Encoding: base64', '', b64, '--BND--', '',
  ].join('\n');
  await smtpInject('relay@exim.local', mail);
  const it = await waitFor(async () => (await list(A, 'inbox')).items.find((i) => i.subject === IN), 'the received message in the inbox');
  inThread = it.thread_id; inMsg = it.id;
  assert.equal(it.from.name, 'Jean Dupont'); assert.equal(it.from.email, 'jean@partner.test'); assert.equal(it.from.id, 0); assert.ok(it.unread); ok('a received message shows up in the inbox of the addressed user, with the external sender');
  assert.ok((await list(B, 'inbox')).items.some((i) => i.subject === IN)); ok('every platform user addressed (To or Cc) receives it');
  const t = await thread(A, inThread);
  const m = t.messages[0];
  assert.ok(m.body.includes('Pouvez-vous nous envoyer un devis')); assert.equal(m.to.length, 1); assert.ok(m.cc.some((p) => p.email === 'tiers@partner.test')); ok('body is decoded and the other recipients are shown');
  assert.equal(m.attachments.length, 1); assert.equal(m.attachments[0].filename, 'devis.txt');
  assert.equal((await download(A, m.attachments[0].id)).buf.toString('utf8'), 'Devis n°42\nTotal: 1000\n'); ok('the attachment of a received message is stored and downloadable');
  assert.equal((await download({ token: (await login('asaid')).token }, m.attachments[0].id)).status, 404); ok('and invisible to people who were not addressed');
}

// ---- duplicates, html-only mail, unknown recipients
{
  const mail = ['From: Jean Dupont <jean@partner.test>', 'To: nyoussouf@exim.local', `Subject: ${IN}`, `Message-ID: <in-${RUN}@partner.test>`, '', 'again'].join('\n');
  await smtpInject('relay@exim.local', mail);
  await sleep(8000);
  assert.equal((await list(A, 'inbox')).items.filter((i) => i.subject === IN).length, 1); ok('the same Message-ID is never filed twice');

  const H = `Newsletter ${RUN}`;
  await smtpInject('relay@exim.local', ['From: News <news@partner.test>', 'To: nyoussouf@exim.local', `Subject: ${H}`, 'Content-Type: text/html; charset=utf-8', '',
    '<html><body><h1>Titre</h1><p>Un <b>paragraphe</b> &amp; un lien <a href="http://x">ici</a></p><script>alert(1)</script></body></html>'].join('\n'));
  const it = await waitFor(async () => (await list(A, 'inbox')).items.find((i) => i.subject === H), 'the html message');
  const body = (await thread(A, it.thread_id)).messages[0].body;
  assert.ok(body.includes('Titre') && body.includes('paragraphe & un lien ici') && !body.includes('<') && !body.includes('alert')); ok('an HTML-only message is turned into safe plain text (no tags, no script)');

  await smtpInject('relay@exim.local', ['From: x@partner.test', 'To: personne@inconnu.test', 'Subject: perdu', '', 'x'].join('\n'));
  await sleep(6000);
  assert.ok(!(await list(A, 'inbox')).items.some((i) => i.subject === 'perdu')); ok('a message for nobody on the platform is skipped');
}

// ---- reply to an external sender stays in the conversation and goes out through the relay
{
  const r = await http(A.token, 'POST', '/mail/send', { to_ext: ['jean@partner.test'], subject: `Re: ${IN}`, body: 'Voici le devis.', parent_id: inMsg });
  assert.equal(r.status, 200, r.text); assert.equal(r.json.thread_id, inThread); ok('replying to an external sender joins the conversation');
  const msgs = await waitFor(async () => { const m = await imapMessages('jean@partner.test', 'x'); return m.find((x) => x.includes(`In-Reply-To: <in-${RUN}@`)) ? m : null; }, 'the reply in the external mailbox');
  const raw = msgs.find((x) => x.includes(`In-Reply-To: <in-${RUN}@`));
  assert.match(raw, new RegExp(`In-Reply-To: <in-${RUN}@partner.test>`, 'i')); ok('the reply carries In-Reply-To so the other side threads it');
}

console.log(`\n${n} checks passed`);
process.exit(0);
