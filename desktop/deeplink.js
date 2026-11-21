'use strict';
// The eximconnect:// links that open the PC application already signed in.
// A link carries ONE thing: a one-time code (43 characters of base64url, valid 60 seconds, usable once).
// Nothing else in the link is ever used: the server address always comes from the application's own settings.

const SCHEME = 'eximconnect';
const CODE = '[A-Za-z0-9_-]{43}';
// Windows may add a slash after the host or at the end: eximconnect://signin/?code=...
const LINK = new RegExp('^' + SCHEME + ':\\/\\/signin\\/?\\?code=(' + CODE + ')\\/?$');

/** the one-time code of a sign-in link, or null (anything that is not exactly such a link is ignored) */
function codeFromLink(link) {
  const m = LINK.exec(String(link ?? '').trim());
  return m ? m[1] : null;
}

/** looks through a command line (Windows passes the link as an argument) for a sign-in link */
function codeFromArgs(args) {
  for (const a of args ?? []) {
    const c = codeFromLink(a);
    if (c) return c;
  }
  return null;
}

/** the page of OUR server that signs the application in with the code */
function signinUrl(server, code) {
  const u = new URL('/', server);
  u.searchParams.set('desktop_code', code);
  return u.toString();
}

/** the page of OUR server that hands the browser session over to the application */
function browserLoginUrl(server) {
  const u = new URL('/', server);
  u.searchParams.set('desktop_login', '1');
  return u.toString();
}

module.exports = { SCHEME, codeFromLink, codeFromArgs, signinUrl, browserLoginUrl };
