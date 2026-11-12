// EximPhone desktop shell: a hardened window around the web app served by the bank's own server.
// TLS is validated against the Windows trust store: install the bank's CA there (never disable checks).
const { app, BrowserWindow, shell, Menu } = require('electron');
const fs = require('fs');
const path = require('path');

const DEFAULT_URL = 'https://localhost';

function serverUrl() {
  if (process.env.EXIM_URL) return process.env.EXIM_URL;
  // Optional per-machine override: %APPDATA%/EximPhone/config.json -> { "url": "https://eximphone.exim.local" }
  try {
    const cfg = JSON.parse(fs.readFileSync(path.join(app.getPath('userData'), 'config.json'), 'utf8'));
    if (typeof cfg.url === 'string') return cfg.url;
  } catch { /* no override */ }
  return DEFAULT_URL;
}

function createWindow(url) {
  const origin = new URL(url).origin;
  const win = new BrowserWindow({
    width: 1280, height: 800, minWidth: 900, minHeight: 600,
    title: 'EximPhone', backgroundColor: '#F4F6FA', show: false,
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true, webSecurity: true },
  });
  win.once('ready-to-show', () => win.show());

  // Stay on the EximPhone server; everything else opens in the system browser.
  win.webContents.on('will-navigate', (e, target) => {
    if (new URL(target).origin !== origin) { e.preventDefault(); void shell.openExternal(target); }
  });
  win.webContents.setWindowOpenHandler(({ url: target }) => {
    void shell.openExternal(target);
    return { action: 'deny' };
  });
  // Microphone is the only permission the app needs (calls, phase 4).
  win.webContents.session.setPermissionRequestHandler((_wc, permission, cb) => cb(permission === 'media'));

  win.loadURL(url).catch(() => {
    win.loadFile(path.join(__dirname, 'offline.html'), { query: { url } });
  });
}

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on('second-instance', () => {
    const [w] = BrowserWindow.getAllWindows();
    if (w) { if (w.isMinimized()) w.restore(); w.focus(); }
  });
  app.whenReady().then(() => {
    Menu.setApplicationMenu(null);
    createWindow(serverUrl());
  });
  app.on('window-all-closed', () => app.quit());
}
