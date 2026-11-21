'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { codeFromLink, codeFromArgs, signinUrl, browserLoginUrl } = require('../deeplink');

const CODE = 'abcDEF0123456789-_abcDEF0123456789-_abcDEF0'; // 43 characters
assert.equal(CODE.length, 43);

test('a sign-in link gives its code', () => {
  assert.equal(codeFromLink(`eximconnect://signin?code=${CODE}`), CODE);
  assert.equal(codeFromLink(`eximconnect://signin/?code=${CODE}`), CODE); // slash added by the system
  assert.equal(codeFromLink(`eximconnect://signin?code=${CODE}/`), CODE);
});

test('anything else is ignored', () => {
  for (const bad of [
    '', null, undefined, 'eximconnect://signin', 'eximconnect://signin?code=short', `eximconnect://signin?code=${CODE}x`,
    `eximconnect://other?code=${CODE}`, `http://signin?code=${CODE}`, `eximconnect://signin?code=${CODE}&url=https://evil.example`,
    `eximconnect://signin?code=${CODE}%00`, `eximconnect://signin?code=${CODE.slice(0, 42)}!`, `xeximconnect://signin?code=${CODE}`,
  ]) assert.equal(codeFromLink(bad), null, String(bad));
});

test('the command line of a protocol launch is searched', () => {
  assert.equal(codeFromArgs(['C:\\Program Files\\Exim Connect\\Exim Connect.exe', '--flag', `eximconnect://signin?code=${CODE}`]), CODE);
  assert.equal(codeFromArgs(['exe', '--flag']), null);
  assert.equal(codeFromArgs(undefined), null);
});

test('the page that signs in always belongs to the configured server, never to the link', () => {
  assert.equal(signinUrl('https://eximphone.example:8443', CODE), `https://eximphone.example:8443/?desktop_code=${CODE}`);
  assert.equal(browserLoginUrl('https://eximphone.example:8443/'), 'https://eximphone.example:8443/?desktop_login=1');
});
