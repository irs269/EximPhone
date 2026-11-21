// Exim Connect desktop shell: a hardened window around the web app served by the bank's own server.
// TLS is validated against the Windows trust store: install the bank's CA there (never disable checks).
//
// It also hosts the one thing a web page cannot do: letting a meeting participant control this computer.
// That stays in this process (see remote.js): a native consent dialog, a permanent banner, a stop shortcut.
const { app, BrowserWindow, shell, Menu, ipcMain, desktopCapturer, screen, dialog, globalShortcut } = require('electron');
const fs = require('fs');
const path = require('path');
const { RemoteControl } = require('./remote');
const { createWinDriver } = require('./winDriver');
const { SCHEME, codeFromArgs, signinUrl, browserLoginUrl } = require('./deeplink');

const DEFAULT_URL = 'https://localhost';
const STOP_SHORTCUT = 'CommandOrControl+Alt+Shift+X';

let mainWin = null;
let origin = '';
let shared = null; // what the person is sharing right now: { controllable, displayId } (a whole screen only is controllable)
let banner = null;
let rc = null;
let pendingCode = process.platform === 'win32' ? codeFromArgs(process.argv) : null; // started by an eximconnect:// link

const french = () => app.getLocale().toLowerCase().startsWith('fr');
const tr = (fr, en) => (french() ? fr : en);

/**
 * Where this installation connects. In order: the EXIM_URL variable, the per-machine file
 * %APPDATA%/Exim Connect/config.json, the defaults baked into the installer by scripts/build-desktop.ps1
 * (defaults.json: the address of the server the installer was downloaded from), then localhost.
 * `pin` is the SHA-256 fingerprint of ONE server certificate to accept even though Windows does not trust it
 * (a test server with a self-signed certificate). Everything else is still validated: checks are never turned off.
 */
function settings() {
  const read = (file) => { try { return JSON.parse(fs.readFileSync(file, 'utf8')); } catch { return {}; } };
  const baked = read(path.join(__dirname, 'defaults.json'));
  const local = read(path.join(app.getPath('userData'), 'config.json'));
  const pick = (k) => (typeof local[k] === 'string' && local[k] ? local[k] : typeof baked[k] === 'string' ? baked[k] : '');
  return { url: process.env.EXIM_URL || pick('url') || DEFAULT_URL, pin: pick('certFingerprint') };
}

const serverUrl = () => settings().url;

/** true when the sender is the Exim Connect page itself (never another site, never a child window). */
function trusted(event) {
  try { return new URL(event.senderFrame.url).origin === origin && event.sender === mainWin?.webContents; } catch { return false; }
}

// ---------- screen picker (replaces the browser's own, which Electron does not provide) ----------

async function pickSource(parent) {
  const sources = await desktopCapturer.getSources({ types: ['screen', 'window'], thumbnailSize: { width: 320, height: 200 }, fetchWindowIcons: false });
  return new Promise((resolve) => {
    const picker = new BrowserWindow({
      parent, modal: true, width: 820, height: 580, resizable: false, minimizable: false, maximizable: false, autoHideMenuBar: true,
      title: tr('Partager', 'Share'), backgroundColor: '#F4F6FA', icon: path.join(__dirname, 'icon.png'),
      webPreferences: { preload: path.join(__dirname, 'ui-preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true },
    });
    let done = false;
    const finish = (src) => {
      if (done) return;
      done = true;
      ipcMain.removeListener('picker:choose', onChoose);
      if (!picker.isDestroyed()) picker.close();
      resolve(src);
    };
    const onChoose = (e, id) => { if (e.sender === picker.webContents) finish(sources.find((s) => s.id === id) ?? null); };
    ipcMain.on('picker:choose', onChoose);
    picker.on('closed', () => finish(null));
    picker.webContents.on('did-finish-load', () => {
      picker.webContents.send('picker:sources', sources.map((s) => ({ id: s.id, name: s.name, thumb: s.thumbnail.toDataURL(), screen: s.id.startsWith('screen:') })), french());
    });
    void picker.loadFile(path.join(__dirname, 'picker.html'));
  });
}

// ---------- remote control ----------

const displayFor = (displayId) => {
  const all = screen.getAllDisplays();
  return all.find((d) => String(d.id) === String(displayId)) ?? screen.getPrimaryDisplay();
};

function showBanner(name) {
  hideBanner();
  const d = displayFor(shared?.displayId);
  const w = 640, h = 46;
  banner = new BrowserWindow({
    x: Math.round(d.bounds.x + (d.bounds.width - w) / 2), y: d.bounds.y + 8, width: w, height: h,
    frame: false, resizable: false, movable: false, minimizable: false, maximizable: false, skipTaskbar: true, focusable: false,
    alwaysOnTop: true, transparent: false, hasShadow: true, show: false,
    webPreferences: { preload: path.join(__dirname, 'ui-preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  banner.setAlwaysOnTop(true, 'screen-saver'); // above full-screen windows too
  banner.once('ready-to-show', () => banner?.showInactive());
  banner.on('closed', () => { banner = null; });
  void banner.loadFile(path.join(__dirname, 'banner.html'), { query: { name, fr: french() ? '1' : '0' } });
}

function hideBanner() {
  if (banner && !banner.isDestroyed()) banner.destroy();
  banner = null;
}

function setupRemoteControl() {
  rc = new RemoteControl({
    driver: createWinDriver(),
    ui: {
      sharedScreen: () => shared,
      displayBounds: (displayId) => {
        const d = displayFor(displayId);
        return d ? screen.dipToScreenRect(null, d.bounds) : null; // the driver works in physical pixels
      },
      // A native dialog of this process: the web page can neither answer it nor imitate it. "Refuse" is the default.
      askConsent: async (name) => {
        const r = await dialog.showMessageBox(mainWin, {
          type: 'warning', buttons: [tr('Refuser', 'Refuse'), tr('Autoriser', 'Allow')], defaultId: 0, cancelId: 0, noLink: true,
          title: 'Exim Connect',
          message: tr(`${name} demande à prendre le contrôle de votre écran.`, `${name} is asking to take control of your screen.`),
          detail: tr(
            'Cette personne pourra déplacer la souris et utiliser le clavier sur votre ordinateur, avec vos droits. Ne l’autorisez que si vous la connaissez et attendez cette demande.\n\nVous pourrez arrêter à tout moment avec le bouton du bandeau rouge ou le raccourci Ctrl+Alt+Maj+X. La session s’arrête seule au bout d’une heure.',
            'This person will be able to move the mouse and use the keyboard on your computer, with your rights. Only allow it if you know them and expected this request.\n\nYou can stop at any time with the button of the red banner or the shortcut Ctrl+Alt+Shift+X. The session ends by itself after one hour.'),
        });
        return r.response === 1;
      },
      showBanner,
      hideBanner,
      onEnded: (reason) => {
        globalShortcut.unregister(STOP_SHORTCUT);
        mainWin?.webContents.send('rc:ended', reason);
      },
    },
  });

  ipcMain.handle('rc:available', (e) => trusted(e) && rc.available);
  ipcMain.handle('rc:sharedScreen', (e) => (trusted(e) ? { controllable: !!shared?.controllable } : { controllable: false }));
  ipcMain.handle('rc:begin', async (e, name) => {
    if (!trusted(e)) return { ok: false, reason: 'untrusted' };
    const r = await rc.begin(name);
    if (r.ok) globalShortcut.register(STOP_SHORTCUT, () => rc.end('stopped_by_owner'));
    return r;
  });
  ipcMain.on('rc:input', (e, ev) => { if (trusted(e)) rc.input(ev); });
  ipcMain.on('rc:end', (e) => { if (trusted(e)) rc.end('stopped'); });
  ipcMain.on('rc:shareStopped', (e) => { if (trusted(e)) { shared = null; rc.end('share_stopped'); } });
  // the button of the banner
  // the sign-in screen: the same hand-over, on demand
  ipcMain.on('auth:browser', (e) => { if (trusted(e)) void shell.openExternal(browserLoginUrl(serverUrl())); });
  ipcMain.on('rc:kill', (e) => { if (banner && e.sender === banner.webContents) rc.end('stopped_by_owner'); });
}

// ---------- signing in without typing the password ----------

/** A one-time code arrived (eximconnect://signin?code=...): the site, on OUR configured address, turns it into a session. */
function openSignin(code) {
  if (mainWin && !mainWin.isDestroyed()) {
    void mainWin.loadURL(signinUrl(serverUrl(), code)).catch(() => undefined);
    if (mainWin.isMinimized()) mainWin.restore();
    mainWin.focus();
  } else {
    pendingCode = code;
  }
}

/** Windows opens eximconnect:// links with this application (registered for the current user, no administrator rights). */
function registerLinks() {
  try { if (app.isPackaged) app.setAsDefaultProtocolClient(SCHEME); } catch { /* the application still works without the links */ }
}

/**
 * Right after the installation the person is usually signed in on the web already: the first time the application starts, the
 * browser opens the site, which hands the session over (when the browser is signed in the application signs in by itself).
 * Never in development, and EXIM_NO_BROWSER_LOGIN=1 turns it off (automated tests).
 */
function firstRunBrowserLogin() {
  if (!app.isPackaged || process.env.EXIM_NO_BROWSER_LOGIN === '1') return;
  const marker = path.join(app.getPath('userData'), 'first-run.json');
  if (fs.existsSync(marker)) return;
  try { fs.writeFileSync(marker, JSON.stringify({ at: new Date().toISOString() })); } catch { return; }
  setTimeout(() => { void shell.openExternal(browserLoginUrl(serverUrl())); }, 2500);
}

// ---------- window ----------

function createWindow(url) {
  origin = new URL(url).origin;
  const win = new BrowserWindow({
    width: 1280, height: 800, minWidth: 900, minHeight: 600,
    title: 'Exim Connect', backgroundColor: '#F4F6FA', show: false, icon: path.join(__dirname, 'icon.png'),
    webPreferences: { preload: path.join(__dirname, 'preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true, webSecurity: true },
  });
  mainWin = win;
  win.once('ready-to-show', () => win.show());
  win.on('closed', () => { rc?.end('closed'); mainWin = null; });

  // Stay on the Exim Connect server; everything else opens in the system browser.
  win.webContents.on('will-navigate', (e, target) => {
    if (new URL(target).origin !== origin) { e.preventDefault(); void shell.openExternal(target); }
  });
  win.webContents.setWindowOpenHandler(({ url: target }) => {
    void shell.openExternal(target);
    return { action: 'deny' };
  });
  // Microphone and camera are the only permissions the app needs (calls and meetings).
  win.webContents.session.setPermissionRequestHandler((_wc, permission, cb) => cb(permission === 'media'));
  // Screen sharing: our own picker. Remote control is only possible for a whole screen.
  win.webContents.session.setDisplayMediaRequestHandler(async (request, callback) => {
    try {
      let from = '';
      try { from = new URL(request.frame.url).origin; } catch { /* no frame */ }
      if (from !== origin) return callback({});
      const choice = await pickSource(win);
      if (!choice) { shared = null; return callback({}); }
      const isScreen = choice.id.startsWith('screen:');
      shared = { controllable: isScreen, displayId: isScreen ? choice.display_id || null : null };
      callback({ video: choice });
    } catch {
      shared = null;
      callback({});
    }
  });

  win.loadURL(url).catch(() => {
    win.loadFile(path.join(__dirname, 'offline.html'), { query: { url } });
  });
}

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on('second-instance', (_e, argv) => {
    const [w] = BrowserWindow.getAllWindows();
    const code = codeFromArgs(argv);
    if (code) openSignin(code);
    if (w) { if (w.isMinimized()) w.restore(); w.focus(); }
  });
  // Accept a self-signed server certificate only when it is exactly the one pinned in the settings.
  app.on('certificate-error', (event, _wc, url, _error, certificate, callback) => {
    const { pin } = settings();
    let sameServer = false;
    // the page is https://host and its WebSocket wss://host: compare the host (and port), not the scheme
    try { sameServer = new URL(url).host === new URL(origin).host; } catch { /* not a URL */ }
    if (pin && sameServer && certificate.fingerprint === pin) { event.preventDefault(); callback(true); } else { callback(false); }
  });
  app.whenReady().then(() => {
    Menu.setApplicationMenu(null);
    setupRemoteControl();
    const first = pendingCode ? signinUrl(serverUrl(), pendingCode) : serverUrl();
    createWindow(first);
    registerLinks();
    if (!pendingCode) firstRunBrowserLogin();
    pendingCode = null;
  });
  app.on('will-quit', () => { globalShortcut.unregisterAll(); rc?.end('closed'); });
  app.on('window-all-closed', () => app.quit());
}
