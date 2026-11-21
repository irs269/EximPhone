// Acceptance test of the hand-over of a web session to the PC application (one-time code).
// Needs the dev stack with the demo directory.
// Usage:  NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/desktop-signin.mjs [https://localhost]
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
  return { status: res.status, json, text, headers: res.headers };
}
async function login(username) {
  for (let i = 0; i < 12; i++) { // nginx limits the login rate: wait and retry
    const r = await http(null, 'POST', '/auth/login', { username, password: PASS });
    if (r.status === 429) { await new Promise((s) => setTimeout(s, 5000)); continue; }
    assert.equal(r.status, 200, `${username}: ${r.text}`);
    return { token: r.json.access_token, id: r.json.user.id, name: username };
  }
  throw new Error('login rate limit did not clear');
}

const A = await login('asaid');

// ---- issuing the code
assert.equal((await http(null, 'POST', '/auth/desktop-code')).status, 401); ok('a code cannot be requested without a signed-in session');
const issued = await http(A.token, 'POST', '/auth/desktop-code');
assert.equal(issued.status, 200, issued.text);
assert.match(issued.json.code, /^[A-Za-z0-9_-]{43}$/); assert.equal(issued.json.expires_in, 60); ok('a signed-in session gets a 43-character code valid for 60 seconds');
const other = await http(A.token, 'POST', '/auth/desktop-code');
assert.notEqual(other.json.code, issued.json.code); ok('every code is different');

// ---- exchanging it
const ex = await http(null, 'POST', '/auth/desktop-exchange', { code: issued.json.code });
assert.equal(ex.status, 200, ex.text);
assert.ok(ex.json.access_token); assert.equal(ex.json.user.id, A.id); assert.equal(ex.json.new_clock_in, false); assert.ok(ex.json.attendance?.id);
ok('the code gives a session of the same user, and it is not a new clock-in');
assert.match(ex.headers.get('set-cookie') ?? '', /refresh=.*HttpOnly/i); ok('the refresh cookie is HttpOnly');
assert.equal((await http(ex.json.access_token, 'GET', '/me')).status, 200); ok('the new access token works');
assert.equal((await http(null, 'POST', '/auth/desktop-exchange', { code: issued.json.code })).status, 401); ok('a code can be used only once');

// ---- what is refused
for (const [label, body] of [
  ['an empty body', undefined], ['no code', {}], ['a short code', { code: 'abc' }], ['a code of the right shape that does not exist', { code: 'A'.repeat(43) }],
  ['a code with forbidden characters', { code: '!'.repeat(43) }], ['a number', { code: 12345 }],
]) {
  const r = await http(null, 'POST', '/auth/desktop-exchange', body);
  assert.equal(r.status, 401, `${label}: ${r.text}`); assert.equal(r.json?.access_token, undefined);
}
ok('malformed or unknown codes are refused (401, nothing disclosed)');

// ---- a closed work day cannot be re-opened with a code
const B = await login('oabdallah');
const stale = (await http(B.token, 'POST', '/auth/desktop-code')).json.code;
assert.equal((await http(B.token, 'POST', '/auth/logout', { reason: 'end of the test' })).status, 200);
const closed = await http(null, 'POST', '/auth/desktop-exchange', { code: stale });
assert.equal(closed.status, 401); assert.equal(closed.json.error, 'session closed'); ok('after clocking out, a code no longer signs in (the password is needed)');
assert.equal((await http(null, 'POST', '/auth/desktop-exchange', { code: stale })).status, 401); ok('and it is consumed anyway');

console.log(`\n${n} checks passed`);
