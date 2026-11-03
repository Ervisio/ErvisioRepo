import assert from "node:assert/strict";
import { test } from "node:test";
import { encodeQR, formatBits, rsRemainder } from "./qr.ts";

test("Reed-Solomon matches the ISO 18004 worked example", () => {
  const data = [0x10, 0x20, 0x0c, 0x56, 0x61, 0x80, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11];
  assert.deepEqual(rsRemainder(data, 10), [0xa5, 0x24, 0xd4, 0xc1, 0xed, 0x36, 0xc7, 0x87, 0x2c, 0x55]);
});

test("format information for level L, mask 0", () => {
  assert.equal(formatBits(0), 0b111011111000100);
});

test("matrix size and finder patterns", () => {
  const m = encodeQR("HELLO");
  assert.equal(m.length, 21);
  for (const [ox, oy] of [[0, 0], [14, 0], [0, 14]] as const) {
    assert.equal(m[oy]![ox], true);
    assert.equal(m[oy + 3]![ox + 3], true);
    assert.equal(m[oy + 1]![ox + 1], false);
  }
});

test("otpauth URL fits and grows the version", () => {
  const url = "otpauth://totp/ErvisioRepo:someone?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=ErvisioRepo&period=30";
  const m = encodeQR(url);
  assert.equal((m.length - 17) % 4, 0);
  assert.ok(m.length > 21 && m.length <= 57);
  assert.throws(() => encodeQR("x".repeat(300)), RangeError);
});
