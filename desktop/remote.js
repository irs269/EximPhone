'use strict';
// Remote control of this computer, controlled from the main process only.
//
// Rules (the web page cannot change any of them):
//  - a session starts only after the person at this computer accepted a NATIVE dialog (askConsent);
//  - only a whole screen that is being shared can be controlled, never a window;
//  - a banner stays on screen for the whole session, with a button and a keyboard shortcut that stop it at once;
//  - the session ends by itself after one hour, when the share stops, and releases every key and button still held;
//  - input is only accepted while a session is active.
// The OS side (moving the pointer, pressing keys) is a `driver`, so that this logic is testable without touching the machine.

const { codeToVk, isTypedChar, MODIFIER_CODES, BLOCKED_CODES } = require('./keymap');

const MAX_SESSION_MS = 60 * 60 * 1000;
const clamp01 = (v) => (Number.isFinite(v) ? Math.min(1, Math.max(0, v)) : 0);

class RemoteControl {
  /**
   * driver: { move(x,y), button(b, down), wheel(dx,dy), key(vk, ext, down), typeChar(ch, held) -> bool }
   * ui: { sharedScreen() -> {controllable, displayId}|null, displayBounds(displayId) -> {x,y,width,height}|null,
   *       askConsent(name) -> Promise<bool>, showBanner(name, bounds), hideBanner(), onEnded(reason) }
   */
  constructor({ driver, ui, setTimer = setTimeout, clearTimer = clearTimeout }) {
    this.driver = driver;
    this.ui = ui;
    this.setTimer = setTimer;
    this.clearTimer = clearTimer;
    this.session = null;
  }

  get available() { return !!this.driver; }
  get active() { return !!this.session; }

  /** Asks the person here; returns {ok:true} once they accepted and the session runs. */
  async begin(controllerName) {
    if (!this.driver) return { ok: false, reason: 'unavailable' };
    if (this.session) return { ok: false, reason: 'busy' };
    const name = String(controllerName ?? '').replace(/[\r\n\u0000-\u001f]/g, ' ').trim().slice(0, 80) || '?';
    const shared = this.ui.sharedScreen();
    if (!shared || !shared.controllable) return { ok: false, reason: 'not_a_screen' };
    if (!(await this.ui.askConsent(name))) return { ok: false, reason: 'refused' };
    // the share may have stopped, or changed, while the dialog was open
    const now = this.ui.sharedScreen();
    const bounds = now && now.controllable ? this.ui.displayBounds(now.displayId) : null;
    if (!bounds || this.session) return { ok: false, reason: 'not_a_screen' };
    this.session = { name, bounds, held: new Set(), buttons: new Set(), timer: null };
    this.session.timer = this.setTimer(() => this.end('expired'), MAX_SESSION_MS);
    this.ui.showBanner(name, bounds);
    return { ok: true };
  }

  /** Applies one event from the controller. Returns false when it was ignored. */
  input(ev) {
    const s = this.session;
    if (!s || !ev || typeof ev !== 'object') return false;
    const { bounds } = s;
    const at = (x, y) => [
      bounds.x + Math.round(clamp01(x) * (bounds.width - 1)),
      bounds.y + Math.round(clamp01(y) * (bounds.height - 1)),
    ];
    const hasXY = Number.isFinite(ev.x) && Number.isFinite(ev.y);
    switch (ev.k) {
      case 'm':
        this.driver.move(...at(ev.x, ev.y));
        return true;
      case 'd': case 'u': {
        const b = ev.b === 1 || ev.b === 2 ? ev.b : 0;
        if (hasXY) this.driver.move(...at(ev.x, ev.y));
        if (ev.k === 'd') s.buttons.add(b); else s.buttons.delete(b);
        this.driver.button(b, ev.k === 'd');
        return true;
      }
      case 'w': {
        const dx = Number(ev.dx) || 0, dy = Number(ev.dy) || 0;
        // browsers count pixels, positive downwards; Windows counts 120 per notch, positive upwards
        this.driver.wheel(Math.round(dx * 1.2), Math.round(-dy * 1.2));
        return true;
      }
      case 'kd': case 'ku': {
        const code = String(ev.code ?? ''), key = ev.key;
        if (BLOCKED_CODES.has(code)) return false;
        const down = ev.k === 'kd';
        if (isTypedChar(key, code)) {
          if (!down) return true; // a character is typed (press and release) on the key-down
          return this.driver.typeChar([...key][0], s.held) !== false;
        }
        const m = codeToVk(code);
        if (!m) return false;
        if (down) s.held.add(m.vk); else s.held.delete(m.vk);
        this.driver.key(m.vk, m.ext, down);
        return true;
      }
      default:
        return false;
    }
  }

  /** Ends the session (stop button, shortcut, expiry, share stopped, meeting left) and releases anything still held. */
  end(reason) {
    const s = this.session;
    if (!s) return;
    this.session = null;
    this.clearTimer(s.timer);
    for (const b of s.buttons) this.driver.button(b, false);
    for (const vk of s.held) this.driver.key(vk, false, false);
    this.ui.hideBanner();
    this.ui.onEnded(reason);
  }
}

module.exports = { RemoteControl, MAX_SESSION_MS, MODIFIER_CODES };
