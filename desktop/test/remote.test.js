'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { RemoteControl, MAX_SESSION_MS } = require('../remote');
const { codeToVk, isTypedChar } = require('../keymap');

function setup({ shared = { controllable: true, displayId: 'd1' }, consent = true, bounds = { x: 0, y: 0, width: 1920, height: 1080 } } = {}) {
  const calls = [];
  const state = { shared, consent, asked: [], banner: [], ended: [], timers: [] };
  const driver = {
    move: (x, y) => calls.push(['move', x, y]),
    button: (b, d) => calls.push(['button', b, d]),
    wheel: (dx, dy) => calls.push(['wheel', dx, dy]),
    key: (vk, ext, d) => calls.push(['key', vk, ext, d]),
    typeChar: (ch, held) => { calls.push(['char', ch, [...held]]); return true; },
  };
  const ui = {
    sharedScreen: () => state.shared,
    displayBounds: () => bounds,
    askConsent: async (name) => { state.asked.push(name); return typeof state.consent === 'function' ? state.consent() : state.consent; },
    showBanner: (name) => state.banner.push(['show', name]),
    hideBanner: () => state.banner.push(['hide']),
    onEnded: (reason) => state.ended.push(reason),
  };
  const rc = new RemoteControl({ driver, ui, setTimer: (fn, ms) => { state.timers.push({ fn, ms }); return state.timers.length; }, clearTimer: () => undefined });
  return { rc, calls, state };
}

test('nothing is possible before the person here accepts', async () => {
  const { rc, calls } = setup();
  assert.equal(rc.input({ k: 'm', x: 0.5, y: 0.5 }), false);
  assert.equal(calls.length, 0);
  assert.equal(rc.active, false);
});

test('a refusal ends it: no session, no input', async () => {
  const { rc, calls, state } = setup({ consent: false });
  assert.deepEqual(await rc.begin('Nadia'), { ok: false, reason: 'refused' });
  assert.equal(rc.input({ k: 'm', x: 1, y: 1 }), false);
  assert.equal(calls.length, 0);
  assert.deepEqual(state.banner, []);
});

test('only a whole screen can be controlled, and the person is not even asked for a window', async () => {
  const { rc, state } = setup({ shared: { controllable: false } });
  assert.deepEqual(await rc.begin('Nadia'), { ok: false, reason: 'not_a_screen' });
  assert.deepEqual(state.asked, []);
  const none = setup({ shared: null });
  assert.deepEqual(await none.rc.begin('Nadia'), { ok: false, reason: 'not_a_screen' });
});

test('the share may stop while the dialog is open', async () => {
  const { rc, state } = setup({ consent: () => { state.shared = null; return true; } });
  assert.deepEqual(await rc.begin('Nadia'), { ok: false, reason: 'not_a_screen' });
  assert.equal(rc.active, false);
});

test('no driver (not Windows, or the library failed): control is unavailable', async () => {
  const rc = new RemoteControl({ driver: null, ui: { sharedScreen: () => ({ controllable: true }) } });
  assert.equal(rc.available, false);
  assert.deepEqual(await rc.begin('x'), { ok: false, reason: 'unavailable' });
});

test('an accepted session shows the banner, takes the name safely and refuses a second session', async () => {
  const { rc, state } = setup();
  assert.deepEqual(await rc.begin('Nadia\r\nYoussouf\u0007'.padEnd(200, 'x')), { ok: true });
  assert.equal(state.asked[0].length <= 80, true);
  assert.ok(!/[\r\n\u0007]/.test(state.asked[0]));
  assert.deepEqual(state.banner[0].slice(0, 1), ['show']);
  assert.deepEqual(await rc.begin('Other'), { ok: false, reason: 'busy' });
  assert.equal(state.timers[0].ms, MAX_SESSION_MS);
});

test('the pointer lands on the shared screen, whichever screen it is', async () => {
  const { rc, calls } = setup({ bounds: { x: 1920, y: 100, width: 1280, height: 720 } });
  await rc.begin('Nadia');
  rc.input({ k: 'm', x: 0, y: 0 });
  rc.input({ k: 'm', x: 1, y: 1 });
  rc.input({ k: 'm', x: 7, y: -3 }); // clamped, never outside the screen
  rc.input({ k: 'm', x: 0.5, y: 0.5 });
  assert.deepEqual(calls, [['move', 1920, 100], ['move', 1920 + 1279, 100 + 719], ['move', 1920 + 1279, 100], ['move', 1920 + 640, 100 + 360]]);
});

test('clicks move first, wheel direction is converted, and buttons never stay pressed after the end', async () => {
  const { rc, calls } = setup();
  await rc.begin('Nadia');
  rc.input({ k: 'd', b: 2, x: 0.25, y: 0.5 });
  assert.deepEqual(calls.slice(0, 2), [['move', 480, 540], ['button', 2, true]]);
  rc.input({ k: 'w', dx: 0, dy: 100 }); // scroll down in the browser = negative on Windows
  assert.deepEqual(calls.at(-1), ['wheel', 0, -120]);
  calls.length = 0;
  rc.end('stopped');
  assert.deepEqual(calls, [['button', 2, false]]);
});

test('typed characters go through the layout-aware path; shortcuts keep their modifiers', async () => {
  const { rc, calls } = setup();
  await rc.begin('Nadia');
  rc.input({ k: 'kd', code: 'ControlLeft', key: 'Control' });
  rc.input({ k: 'kd', code: 'KeyQ', key: 'a' }); // AZERTY controller pressing Ctrl+A: the character decides, not the position
  rc.input({ k: 'ku', code: 'KeyQ', key: 'a' });
  rc.input({ k: 'ku', code: 'ControlLeft', key: 'Control' });
  assert.deepEqual(calls, [['key', 0xa2, false, true], ['char', 'a', [0xa2]], ['key', 0xa2, false, false]]);
});

test('navigation keys are flagged extended; function keys and unknown keys behave', async () => {
  const { rc, calls } = setup();
  await rc.begin('Nadia');
  assert.equal(rc.input({ k: 'kd', code: 'ArrowLeft', key: 'ArrowLeft' }), true);
  assert.equal(rc.input({ k: 'kd', code: 'F5', key: 'F5' }), true);
  assert.equal(rc.input({ k: 'kd', code: 'Enter', key: 'Enter' }), true);
  assert.equal(rc.input({ k: 'kd', code: 'LaunchMail', key: 'LaunchMail' }), false);
  assert.deepEqual(calls.slice(0, 3), [['key', 0x25, true, true], ['key', 0x74, false, true], ['key', 0x0d, false, true]]);
});

test('the Windows key is never injected', async () => {
  const { rc, calls } = setup();
  await rc.begin('Nadia');
  for (const code of ['MetaLeft', 'MetaRight', 'OSLeft']) assert.equal(rc.input({ k: 'kd', code, key: 'Meta' }), false);
  assert.equal(calls.length, 0);
});

test('malformed events are ignored', async () => {
  const { rc, calls } = setup();
  await rc.begin('Nadia');
  for (const ev of [null, 'x', {}, { k: 'exec' }, { k: 'kd' }, { k: 'kd', code: 42 }]) assert.equal(rc.input(ev), false);
  assert.equal(calls.length, 0);
});

test('ending releases held keys, hides the banner, reports the reason and blocks further input', async () => {
  const { rc, calls, state } = setup();
  await rc.begin('Nadia');
  rc.input({ k: 'kd', code: 'ShiftLeft', key: 'Shift' });
  rc.input({ k: 'kd', code: 'AltLeft', key: 'Alt' });
  calls.length = 0;
  rc.end('share_stopped');
  assert.deepEqual(calls.sort(), [['key', 0xa0, false, false], ['key', 0xa4, false, false]].sort());
  assert.deepEqual(state.banner.at(-1), ['hide']);
  assert.deepEqual(state.ended, ['share_stopped']);
  assert.equal(rc.input({ k: 'm', x: 0.1, y: 0.1 }), false);
  rc.end('again'); // ending twice does nothing
  assert.deepEqual(state.ended, ['share_stopped']);
});

test('a session expires by itself after an hour', async () => {
  const { rc, state } = setup();
  await rc.begin('Nadia');
  state.timers[0].fn();
  assert.deepEqual(state.ended, ['expired']);
  assert.equal(rc.active, false);
});

test('key map', () => {
  assert.deepEqual(codeToVk('NumpadEnter'), { vk: 0x0d, ext: true });
  assert.deepEqual(codeToVk('Digit1'), null); // digits are typed characters, handled by the layout-aware path
  assert.equal(isTypedChar('é', 'Digit2'), true);
  assert.equal(isTypedChar('Enter', 'Enter'), false);
  assert.equal(isTypedChar(' ', 'Space'), false); // Space is in the table: pressed as a key
  assert.equal(isTypedChar('Dead', 'BracketLeft'), false);
});
