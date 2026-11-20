'use strict';
// Preload of the two small native windows (screen picker and control banner).
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('nativeUi', {
  onSources: (cb) => { ipcRenderer.on('picker:sources', (_e, sources, french) => cb(sources, french)); },
  choose: (id) => ipcRenderer.send('picker:choose', id),
  stopControl: () => ipcRenderer.send('rc:kill'),
});
