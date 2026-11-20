'use strict';
// Maps KeyboardEvent.code of the controller to Windows virtual-key codes, for the keys that are not typed characters
// (Enter, arrows, function keys, modifiers...). Typed characters are handled by the driver from KeyboardEvent.key, so
// that they come out right whatever the keyboard layouts of the two people are.
// `ext` marks the "extended" keys (right-hand and navigation keys), which Windows expects flagged as such.
//
// The Windows key is deliberately absent: a remote person must not be able to open the Start menu, Win+R or Win+L.

const table = {
  Enter: [0x0d], NumpadEnter: [0x0d, true], Escape: [0x1b], Space: [0x20], Tab: [0x09], Backspace: [0x08], CapsLock: [0x14],
  ContextMenu: [0x5d, true],
  Delete: [0x2e, true], Insert: [0x2d, true], Home: [0x24, true], End: [0x23, true], PageUp: [0x21, true], PageDown: [0x22, true],
  ArrowLeft: [0x25, true], ArrowUp: [0x26, true], ArrowRight: [0x27, true], ArrowDown: [0x28, true],
  ShiftLeft: [0xa0], ShiftRight: [0xa1], ControlLeft: [0xa2], ControlRight: [0xa3, true], AltLeft: [0xa4], AltRight: [0xa5, true],
  NumpadAdd: [0x6b], NumpadSubtract: [0x6d], NumpadMultiply: [0x6a], NumpadDivide: [0x6f, true], NumpadDecimal: [0x6e],
};
for (let i = 1; i <= 12; i++) table['F' + i] = [0x6f + i];
for (let i = 0; i <= 9; i++) table['Numpad' + i] = [0x60 + i];

/** @returns {{vk:number, ext:boolean}|null} */
function codeToVk(code) {
  const e = table[code];
  return e ? { vk: e[0], ext: !!e[1] } : null;
}

const MODIFIER_CODES = new Set(['ShiftLeft', 'ShiftRight', 'ControlLeft', 'ControlRight', 'AltLeft', 'AltRight']);
const BLOCKED_CODES = new Set(['MetaLeft', 'MetaRight', 'OSLeft', 'OSRight']);

/** A key that produces a character on its own (not a modifier or a function key). */
function isTypedChar(key, code) {
  return typeof key === 'string' && [...key].length === 1 && !(code in table);
}

module.exports = { codeToVk, isTypedChar, MODIFIER_CODES, BLOCKED_CODES };
