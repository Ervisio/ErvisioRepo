// CodeMirror 6 setup, loaded only by the editor page through a dynamic import.
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { bracketMatching, HighlightStyle, indentOnInput, syntaxHighlighting, type LanguageSupport } from "@codemirror/language";
import { Compartment, EditorState } from "@codemirror/state";
import { drawSelection, EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from "@codemirror/view";
import { tags } from "@lezer/highlight";
import { cspNonce } from "../nonce.ts";

const v = (name: string) => `var(--syn-${name})`;

// Same --syn-* tokens as the Shiki theme used by the file view.
const style = HighlightStyle.define([
  { tag: [tags.comment, tags.lineComment, tags.blockComment, tags.docComment], color: v("com"), fontStyle: "italic" },
  { tag: [tags.keyword, tags.controlKeyword, tags.moduleKeyword, tags.operatorKeyword, tags.definitionKeyword, tags.modifier, tags.self, tags.tagName, tags.heading], color: v("kw") },
  { tag: [tags.string, tags.special(tags.string), tags.regexp, tags.monospace], color: v("str") },
  { tag: [tags.number, tags.bool, tags.null, tags.atom, tags.constant(tags.name), tags.strong], color: v("num") },
  { tag: [tags.function(tags.variableName), tags.function(tags.propertyName), tags.attributeName, tags.link, tags.url], color: v("fn") },
  { tag: [tags.typeName, tags.className, tags.namespace, tags.standard(tags.typeName), tags.propertyName], color: v("type") },
  { tag: tags.emphasis, fontStyle: "italic" },
  { tag: tags.inserted, color: "var(--ok)" },
  { tag: tags.deleted, color: "var(--err)" },
  { tag: tags.invalid, color: "var(--err)" },
]);

const theme = EditorView.theme({
  "&": { color: "var(--ink)", backgroundColor: "var(--bg)", fontSize: "12.75px", borderRadius: "12px" },
  "&.cm-focused": { outline: "2px solid var(--hue)", outlineOffset: "-2px" },
  ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "1.65", minHeight: "320px" },
  ".cm-content": { caretColor: "var(--ink)", padding: "10px 0" },
  ".cm-gutters": { backgroundColor: "var(--bg)", color: "var(--ink3)", border: "none", borderRadius: "12px 0 0 12px" },
  ".cm-activeLine, .cm-activeLineGutter": { backgroundColor: "var(--sunk)" },
  ".cm-cursor": { borderLeftColor: "var(--ink)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": { backgroundColor: "var(--hue-s)" },
  ".cm-matchingBracket": { backgroundColor: "var(--raise)", outline: "1px solid var(--ink3)" },
});

type Loader = () => Promise<LanguageSupport>;

// One lazy chunk per language pack.
const byExt: Record<string, Loader> = {};
const add = (exts: string[], load: Loader) => exts.forEach((e) => (byExt[e] = load));
add(["js", "mjs", "cjs"], () => import("@codemirror/lang-javascript").then((m) => m.javascript()));
add(["jsx"], () => import("@codemirror/lang-javascript").then((m) => m.javascript({ jsx: true })));
add(["ts", "mts", "cts"], () => import("@codemirror/lang-javascript").then((m) => m.javascript({ typescript: true })));
add(["tsx"], () => import("@codemirror/lang-javascript").then((m) => m.javascript({ jsx: true, typescript: true })));
add(["json", "jsonc"], () => import("@codemirror/lang-json").then((m) => m.json()));
add(["css", "scss", "less"], () => import("@codemirror/lang-css").then((m) => m.css()));
add(["html", "htm", "vue", "svelte"], () => import("@codemirror/lang-html").then((m) => m.html()));
add(["md", "markdown"], () => import("@codemirror/lang-markdown").then((m) => m.markdown()));
add(["py", "pyi"], () => import("@codemirror/lang-python").then((m) => m.python()));
add(["go"], () => import("@codemirror/lang-go").then((m) => m.go()));
add(["yaml", "yml"], () => import("@codemirror/lang-yaml").then((m) => m.yaml()));

function languageFor(path: string): Loader | undefined {
  const base = (path.split("/").pop() ?? "").toLowerCase();
  const dot = base.lastIndexOf(".");
  return dot > 0 ? byExt[base.slice(dot + 1)] : undefined;
}

export type EditorHandle = { view: EditorView; setPath(path: string): void; destroy(): void };

/** Mounts an editor in `parent`. `onChange` receives the whole document after edits. */
export function createEditor(parent: HTMLElement, doc: string, path: string, label: string, onChange: (doc: string) => void): EditorHandle {
  const lang = new Compartment();
  const view = new EditorView({
    parent,
    state: EditorState.create({
      doc,
      extensions: [
        EditorView.cspNonce.of(cspNonce() ?? ""),
        lineNumbers(),
        highlightActiveLineGutter(),
        highlightActiveLine(),
        drawSelection(),
        history(),
        indentOnInput(),
        bracketMatching(),
        syntaxHighlighting(style),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        theme,
        lang.of([]),
        EditorView.contentAttributes.of({ "aria-label": label }),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) onChange(u.state.doc.toString());
        }),
      ],
    }),
  });
  let current = "";
  const setPath = (p: string) => {
    const load = languageFor(p);
    current = p;
    if (!load) {
      view.dispatch({ effects: lang.reconfigure([]) });
      return;
    }
    void load().then((support) => {
      if (current === p) view.dispatch({ effects: lang.reconfigure(support) });
    });
  };
  setPath(path);
  return { view, setPath, destroy: () => view.destroy() };
}
