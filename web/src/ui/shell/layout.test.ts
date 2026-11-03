import assert from "node:assert/strict";
import { test } from "node:test";
import { parseLayout } from "./layout.ts";

test("parseLayout accepts the three layouts", () => {
  assert.equal(parseLayout("console"), "console");
  assert.equal(parseLayout("foglio"), "foglio");
  assert.equal(parseLayout("corsie"), "corsie");
});

test("parseLayout falls back to console", () => {
  assert.equal(parseLayout(null), "console");
  assert.equal(parseLayout("grid"), "console");
  assert.equal(parseLayout(undefined), "console");
});
