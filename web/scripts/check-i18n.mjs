// Fails when en/it catalogs differ or when code uses a key that does not exist.
// Usage in code: `const t = useT("ns")` followed by `t("key")`.
import { readdirSync, readFileSync } from "node:fs";
import { basename, extname, join } from "node:path";
import { fileURLToPath } from "node:url";

const locales = ["en", "it"];

function loadCatalogs(i18nDir) {
  const catalogs = {};
  for (const locale of locales) {
    catalogs[locale] = {};
    for (const file of readdirSync(join(i18nDir, locale))) {
      if (extname(file) !== ".json") continue;
      catalogs[locale][basename(file, ".json")] = JSON.parse(readFileSync(join(i18nDir, locale, file), "utf8"));
    }
  }
  return catalogs;
}

function* sourceFiles(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* sourceFiles(path);
    else if (/\.(ts|tsx)$/.test(entry.name) && !/\.(test|d)\.ts$/.test(entry.name)) yield path;
  }
}

/** Returns the list of problems found under srcDir (which holds i18n/ and the code). */
export function checkI18n(srcDir) {
  const errors = [];
  const catalogs = loadCatalogs(join(srcDir, "i18n"));
  const [base, ...others] = locales;

  const namespaces = new Set(locales.flatMap((l) => Object.keys(catalogs[l])));
  for (const ns of namespaces) {
    const baseKeys = new Set(Object.keys(catalogs[base][ns] ?? {}));
    for (const locale of others) {
      const keys = new Set(Object.keys(catalogs[locale][ns] ?? {}));
      for (const k of baseKeys) if (!keys.has(k)) errors.push(`${locale}/${ns}.json: missing key "${k}"`);
      for (const k of keys) if (!baseKeys.has(k)) errors.push(`${locale}/${ns}.json: extra key "${k}" not in ${base}`);
    }
  }

  for (const file of sourceFiles(srcDir)) {
    const code = readFileSync(file, "utf8");
    for (const [, fn, ns] of code.matchAll(/\b(?:const|let)\s+(\w+)\s*=\s*useT\(\s*["'](\w+)["']\s*\)/g)) {
      const known = catalogs[base][ns];
      if (!known) {
        errors.push(`${file}: unknown namespace "${ns}"`);
        continue;
      }
      for (const [, key] of code.matchAll(new RegExp(`\\b${fn}\\(\\s*["']([^"']+)["']`, "g"))) {
        if (!(key in known)) errors.push(`${file}: unknown key "${key}" in namespace "${ns}"`);
      }
    }
  }
  return errors;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const errors = checkI18n(fileURLToPath(new URL("../src", import.meta.url)));
  for (const e of errors) console.error(e);
  if (errors.length > 0) process.exit(1);
  console.log("i18n ok");
}
