'use strict';
// Exposed to the Exim Connect web page (and only to it). It cannot grant control: the person at this computer
// answers a native dialog opened by the main process, which also owns the session, the banner and the stop shortcut.
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('eximDesktop', {
  version: 3,
  /** opens the system browser on the site: if the person is signed in there, the session comes back to this application */
  signInWithBrowser: () => ipcRenderer.send('auth:browser'),
  remoteControl: {
    /** true when this computer can be controlled (Windows, driver loaded) */
    available: () => ipcRenderer.invoke('rc:available'),
    /** asks the person here (native dialog); resolves {ok} */
    begin: (controllerName) => ipcRenderer.invoke('rc:begin', String(controllerName ?? '')),
    /** one input event from the controller; ignored unless a session is active */
    input: (ev) => ipcRenderer.send('rc:input', ev),
    /** the session stops (also called when the screen share stops) */
    end: () => ipcRenderer.send('rc:end'),
    /** whether what is shared right now is a whole screen (and so can be controlled) */
    sharedScreen: () => ipcRenderer.invoke('rc:sharedScreen'),
    /** the share stopped */
    shareStopped: () => ipcRenderer.send('rc:shareStopped'),
    /** the session ended on this side (stop button, shortcut, expiry): reason is a short code */
    onEnded: (cb) => { ipcRenderer.on('rc:ended', (_e, reason) => cb(reason)); },
  },
});
