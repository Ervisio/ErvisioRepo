// Lazy syntax highlighting with Shiki. Everything here is loaded on demand: the core,
// the JavaScript regex engine (the CSP forbids the WebAssembly Oniguruma engine) and
// one chunk per grammar. Colors are the --syn-* tokens, so light/dark follow the theme.
import type { HighlighterCore, LanguageInput, ThemeRegistrationRaw } from "shiki/core";

export type Token = { content: string; color?: string; italic?: boolean; bold?: boolean };

type Loader = () => Promise<{ default: LanguageInput }>;

// Explicit loaders: the bundler needs static specifiers to emit one chunk per grammar.
const loaders: Record<string, Loader> = {
  astro: () => import("shiki/langs/astro.mjs"),
  bat: () => import("shiki/langs/bat.mjs"),
  c: () => import("shiki/langs/c.mjs"),
  clojure: () => import("shiki/langs/clojure.mjs"),
  cmake: () => import("shiki/langs/cmake.mjs"),
  cpp: () => import("shiki/langs/cpp.mjs"),
  csharp: () => import("shiki/langs/csharp.mjs"),
  css: () => import("shiki/langs/css.mjs"),
  dart: () => import("shiki/langs/dart.mjs"),
  diff: () => import("shiki/langs/diff.mjs"),
  dockerfile: () => import("shiki/langs/dockerfile.mjs"),
  elixir: () => import("shiki/langs/elixir.mjs"),
  erlang: () => import("shiki/langs/erlang.mjs"),
  fish: () => import("shiki/langs/fish.mjs"),
  go: () => import("shiki/langs/go.mjs"),
  graphql: () => import("shiki/langs/graphql.mjs"),
  groovy: () => import("shiki/langs/groovy.mjs"),
  haskell: () => import("shiki/langs/haskell.mjs"),
  hcl: () => import("shiki/langs/hcl.mjs"),
  html: () => import("shiki/langs/html.mjs"),
  ini: () => import("shiki/langs/ini.mjs"),
  java: () => import("shiki/langs/java.mjs"),
  javascript: () => import("shiki/langs/javascript.mjs"),
  json: () => import("shiki/langs/json.mjs"),
  jsonc: () => import("shiki/langs/jsonc.mjs"),
  jsx: () => import("shiki/langs/jsx.mjs"),
  julia: () => import("shiki/langs/julia.mjs"),
  kotlin: () => import("shiki/langs/kotlin.mjs"),
  less: () => import("shiki/langs/less.mjs"),
  lua: () => import("shiki/langs/lua.mjs"),
  makefile: () => import("shiki/langs/makefile.mjs"),
  markdown: () => import("shiki/langs/markdown.mjs"),
  nginx: () => import("shiki/langs/nginx.mjs"),
  nix: () => import("shiki/langs/nix.mjs"),
  ocaml: () => import("shiki/langs/ocaml.mjs"),
  perl: () => import("shiki/langs/perl.mjs"),
  php: () => import("shiki/langs/php.mjs"),
  powershell: () => import("shiki/langs/powershell.mjs"),
  prisma: () => import("shiki/langs/prisma.mjs"),
  proto: () => import("shiki/langs/proto.mjs"),
  python: () => import("shiki/langs/python.mjs"),
  r: () => import("shiki/langs/r.mjs"),
  ruby: () => import("shiki/langs/ruby.mjs"),
  rust: () => import("shiki/langs/rust.mjs"),
  scala: () => import("shiki/langs/scala.mjs"),
  scss: () => import("shiki/langs/scss.mjs"),
  shellscript: () => import("shiki/langs/shellscript.mjs"),
  sql: () => import("shiki/langs/sql.mjs"),
  svelte: () => import("shiki/langs/svelte.mjs"),
  swift: () => import("shiki/langs/swift.mjs"),
  terraform: () => import("shiki/langs/terraform.mjs"),
  toml: () => import("shiki/langs/toml.mjs"),
  tsx: () => import("shiki/langs/tsx.mjs"),
  typescript: () => import("shiki/langs/typescript.mjs"),
  vue: () => import("shiki/langs/vue.mjs"),
  xml: () => import("shiki/langs/xml.mjs"),
  yaml: () => import("shiki/langs/yaml.mjs"),
  zig: () => import("shiki/langs/zig.mjs"),
};

const byExt: Record<string, string> = {
  astro: "astro", bat: "bat", cmd: "bat", c: "c", h: "c", clj: "clojure", cljs: "clojure", cmake: "cmake",
  cc: "cpp", cpp: "cpp", cxx: "cpp", hh: "cpp", hpp: "cpp", cs: "csharp", css: "css", dart: "dart",
  diff: "diff", patch: "diff", ex: "elixir", exs: "elixir", erl: "erlang", fish: "fish", go: "go",
  graphql: "graphql", gql: "graphql", groovy: "groovy", gradle: "groovy", hs: "haskell", hcl: "hcl",
  html: "html", htm: "html", ini: "ini", cfg: "ini", conf: "ini", java: "java", js: "javascript",
  mjs: "javascript", cjs: "javascript", json: "json", jsonc: "jsonc", json5: "jsonc", jsx: "jsx",
  jl: "julia", kt: "kotlin", kts: "kotlin", less: "less", lua: "lua", mk: "makefile", md: "markdown",
  markdown: "markdown", nix: "nix", ml: "ocaml", mli: "ocaml", pl: "perl", pm: "perl", php: "php",
  ps1: "powershell", prisma: "prisma", proto: "proto", py: "python", pyi: "python", r: "r",
  rb: "ruby", rs: "rust", scala: "scala", scss: "scss", sh: "shellscript", bash: "shellscript",
  zsh: "shellscript", sql: "sql", svelte: "svelte", swift: "swift", tf: "terraform", tfvars: "terraform",
  toml: "toml", tsx: "tsx", ts: "typescript", mts: "typescript", cts: "typescript", vue: "vue",
  xml: "xml", svg: "xml", plist: "xml", yaml: "yaml", yml: "yaml", zig: "zig",
};

const byName: Record<string, string> = {
  dockerfile: "dockerfile", containerfile: "dockerfile", makefile: "makefile", gnumakefile: "makefile",
  "cmakelists.txt": "cmake", gemfile: "ruby", rakefile: "ruby", "nginx.conf": "nginx", "go.mod": "go",
  ".bashrc": "shellscript", ".zshrc": "shellscript", ".profile": "shellscript", ".gitconfig": "ini",
  ".editorconfig": "ini", "tsconfig.json": "jsonc", "jsconfig.json": "jsonc",
};

/** Shiki language id for a file path, or undefined when it is not known. */
export function languageOf(path: string): string | undefined {
  const base = (path.split("/").pop() ?? "").toLowerCase();
  if (byName[base]) return byName[base];
  if (base.startsWith("dockerfile")) return "dockerfile";
  const dot = base.lastIndexOf(".");
  return dot > 0 ? byExt[base.slice(dot + 1)] : undefined;
}

const v = (name: string) => `var(--syn-${name})`;

/** Theme built from the --syn-* tokens (Shiki keeps var() colors as-is). */
const theme: ThemeRegistrationRaw = {
  name: "ervisio",
  type: "dark",
  fg: "var(--ink)",
  bg: "var(--bg)",
  settings: [
    { settings: { foreground: "var(--ink)", background: "var(--bg)" } },
    { scope: ["comment", "punctuation.definition.comment", "string.comment"], settings: { foreground: v("com") } },
    {
      scope: [
        "keyword", "storage", "storage.type", "storage.modifier", "keyword.control", "keyword.operator.new",
        "keyword.operator.expression", "keyword.operator.logical.python", "variable.language", "entity.name.tag",
        "markup.heading", "punctuation.definition.heading",
      ],
      settings: { foreground: v("kw") },
    },
    { scope: ["string", "string.quoted", "string.template", "string.regexp", "markup.inline.raw", "markup.fenced_code"], settings: { foreground: v("str") } },
    {
      scope: ["constant.numeric", "constant.language", "constant.character", "constant.other", "support.constant", "variable.other.constant", "markup.bold"],
      settings: { foreground: v("num") },
    },
    {
      scope: ["entity.name.function", "support.function", "meta.function-call entity.name", "variable.function", "entity.other.attribute-name", "markup.underline.link"],
      settings: { foreground: v("fn") },
    },
    {
      scope: [
        "entity.name.type", "entity.name.class", "entity.name.namespace", "support.type", "support.class",
        "entity.other.inherited-class", "storage.type.primitive", "storage.type.built-in", "keyword.type",
        "support.type.property-name", "meta.object-literal.key",
      ],
      settings: { foreground: v("type") },
    },
    { scope: ["markup.italic"], settings: { fontStyle: "italic" } },
    { scope: ["markup.inserted"], settings: { foreground: "var(--ok)" } },
    { scope: ["markup.deleted"], settings: { foreground: "var(--err)" } },
  ],
};

let core: Promise<HighlighterCore> | undefined;

function highlighter(): Promise<HighlighterCore> {
  core ??= (async () => {
    const [{ createHighlighterCore }, { createJavaScriptRegexEngine }] = await Promise.all([
      import("shiki/core"),
      import("shiki/engine/javascript"),
    ]);
    return createHighlighterCore({ themes: [theme], langs: [], engine: createJavaScriptRegexEngine({ forgiving: true }) });
  })();
  return core;
}

// Above this size highlighting would block the main thread for too long: plain text.
const MAX_CHARS = 512 * 1024;
const MAX_LINES = 20_000;

/**
 * Tokenizes code for the given path. Resolves to undefined when the language is unknown
 * or the file is too large; callers keep showing plain text in that case.
 */
export async function highlight(code: string, path: string): Promise<Token[][] | undefined> {
  const lang = languageOf(path);
  const load = lang ? loaders[lang] : undefined;
  if (!lang || !load || code.length > MAX_CHARS) return undefined;
  let lines = 1;
  for (let i = 0; i < code.length; i++) if (code.charCodeAt(i) === 10 && ++lines > MAX_LINES) return undefined;
  const hl = await highlighter();
  if (!hl.getLoadedLanguages().includes(lang)) await hl.loadLanguage((await load()).default);
  const rows = hl.codeToTokensBase(code, { lang, theme: "ervisio" });
  return rows.map((row) =>
    row.map((t) => ({
      content: t.content,
      color: t.color && t.color !== "var(--ink)" ? t.color : undefined,
      italic: ((t.fontStyle ?? 0) & 1) !== 0 || undefined,
      bold: ((t.fontStyle ?? 0) & 2) !== 0 || undefined,
    })),
  );
}
