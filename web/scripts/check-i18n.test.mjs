import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { checkI18n } from "./check-i18n.mjs";

function fixture({ en, it, code }) {
  const src = mkdtempSync(join(tmpdir(), "i18n-"));
  for (const [locale, messages] of [["en", en], ["it", it]]) {
    mkdirSync(join(src, "i18n", locale), { recursive: true });
    writeFileSync(join(src, "i18n", locale, "home.json"), JSON.stringify(messages));
  }
  writeFileSync(join(src, "page.tsx"), code);
  return src;
}

const page = `const t = useT("home"); t("a"); t('b');`;

test("passes when catalogs match and keys exist", () => {
  assert.deepEqual(checkI18n(fixture({ en: { a: "A", b: "B" }, it: { a: "A", b: "B" }, code: page })), []);
});

test("reports keys missing from a locale", () => {
  const errors = checkI18n(fixture({ en: { a: "A", b: "B" }, it: { a: "A" }, code: page }));
  assert.equal(errors.length, 1);
  assert.match(errors[0], /it\/home\.json: missing key "b"/);
});

test("reports extra keys in a locale", () => {
  const errors = checkI18n(fixture({ en: { a: "A", b: "B" }, it: { a: "A", b: "B", c: "C" }, code: page }));
  assert.match(errors.join("\n"), /extra key "c"/);
});

test("reports keys used in code but not defined", () => {
  const errors = checkI18n(fixture({ en: { a: "A" }, it: { a: "A" }, code: page }));
  assert.match(errors.join("\n"), /unknown key "b"/);
});
