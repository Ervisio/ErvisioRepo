import { test } from "node:test";
import assert from "node:assert/strict";
import { csrfToken, needsCSRF } from "./csrf.ts";

test("csrfToken reads either cookie name", () => {
  assert.equal(csrfToken("a=1; __Host-erepo_csrf=tok; b=2"), "tok");
  assert.equal(csrfToken("erepo_csrf=plain"), "plain");
  assert.equal(csrfToken("erepo_csrfx=no; other=1"), undefined);
  assert.equal(csrfToken(""), undefined);
});

test("needsCSRF only for unsafe methods", () => {
  assert.equal(needsCSRF("get"), false);
  assert.equal(needsCSRF("HEAD"), false);
  assert.equal(needsCSRF("POST"), true);
  assert.equal(needsCSRF("DELETE"), true);
});
