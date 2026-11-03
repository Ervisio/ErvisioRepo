import assert from "node:assert/strict";
import { test } from "node:test";
import { fromBase64url, toBase64url } from "./base64url.ts";

test("base64url round trip without padding", () => {
  for (const n of [0, 1, 2, 3, 4, 31, 32]) {
    const bytes = Uint8Array.from({ length: n }, (_, i) => (i * 37 + 250) & 0xff);
    const s = toBase64url(bytes);
    assert.match(s, /^[A-Za-z0-9_-]*$/);
    assert.deepEqual(fromBase64url(s), bytes);
  }
});

test("uses the url alphabet", () => {
  assert.equal(toBase64url(Uint8Array.from([0xfb, 0xff])), "-_8");
});
