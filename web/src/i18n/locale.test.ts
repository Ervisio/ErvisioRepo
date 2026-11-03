import assert from "node:assert/strict";
import { test } from "node:test";
import { format, resolveLocale } from "./locale.ts";

test("resolveLocale picks the first supported language", () => {
  assert.equal(resolveLocale(["it-IT", "en-US"]), "it");
  assert.equal(resolveLocale(["fr-FR", "IT"]), "it");
  assert.equal(resolveLocale(["en-GB"]), "en");
});

test("resolveLocale falls back to en", () => {
  assert.equal(resolveLocale([]), "en");
  assert.equal(resolveLocale(["de", "fr-CA"]), "en");
});

test("format replaces known placeholders only", () => {
  assert.equal(format("Hi {name}, {x}", { name: "Ada" }), "Hi Ada, {x}");
  assert.equal(format("plain"), "plain");
});
