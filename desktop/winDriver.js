'use strict';
// Windows input driver: a handful of user32 calls through koffi (no input-automation library).
// Only loaded on Windows, and only when a remote-control session is accepted.

const MOUSEEVENTF = { down: [0x0002, 0x0020, 0x0008], up: [0x0004, 0x0040, 0x0010], WHEEL: 0x0800, HWHEEL: 0x1000 }; // left, middle, right
const KEYEVENTF_EXTENDEDKEY = 0x0001;
const KEYEVENTF_KEYUP = 0x0002;
const VK_SHIFT = 0xa0, VK_CONTROL = 0xa2, VK_MENU = 0xa4;

function createWinDriver() {
  if (process.platform !== 'win32') return null;
  let user32;
  try {
    const koffi = require('koffi');
    user32 = koffi.load('user32.dll');
    const SetCursorPos = user32.func('bool __stdcall SetCursorPos(int x, int y)');
    const mouse_event = user32.func('void __stdcall mouse_event(uint32 flags, uint32 dx, uint32 dy, uint32 data, uintptr_t extra)');
    const keybd_event = user32.func('void __stdcall keybd_event(uint8 vk, uint8 scan, uint32 flags, uintptr_t extra)');
    const VkKeyScanW = user32.func('int16 __stdcall VkKeyScanW(uint16 ch)');
    const MapVirtualKeyW = user32.func('uint32 __stdcall MapVirtualKeyW(uint32 code, uint32 mapType)');

    const key = (vk, ext, down) => keybd_event(vk, MapVirtualKeyW(vk, 0), (ext ? KEYEVENTF_EXTENDEDKEY : 0) | (down ? 0 : KEYEVENTF_KEYUP), 0);
    return {
      move: (x, y) => { SetCursorPos(x, y); },
      button: (b, down) => mouse_event((down ? MOUSEEVENTF.down : MOUSEEVENTF.up)[b] ?? MOUSEEVENTF.down[0], 0, 0, 0, 0),
      wheel: (dx, dy) => {
        if (dy) mouse_event(MOUSEEVENTF.WHEEL, 0, 0, dy >>> 0, 0); // two's complement for negative deltas
        if (dx) mouse_event(MOUSEEVENTF.HWHEEL, 0, 0, dx >>> 0, 0);
      },
      key,
      /** Types one character the way this keyboard layout produces it, adding Shift/Ctrl/Alt only when the layout needs them. */
      typeChar: (ch, held) => {
        const r = VkKeyScanW(ch.charCodeAt(0));
        if (r === -1) return false; // no key produces this character on this layout
        const vk = r & 0xff, shift = (r >> 8) & 0xff;
        const need = [[1, VK_SHIFT], [2, VK_CONTROL], [4, VK_MENU]].filter(([bit, mod]) => (shift & bit) && !held.has(mod) && !held.has(mod + 1));
        need.forEach(([, mod]) => key(mod, false, true));
        key(vk, false, true);
        key(vk, false, false);
        need.reverse().forEach(([, mod]) => key(mod, false, false));
        return true;
      },
    };
  } catch (e) {
    console.error('remote control driver unavailable:', e.message);
    return null;
  }
}

module.exports = { createWinDriver };
