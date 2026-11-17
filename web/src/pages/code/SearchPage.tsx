import { Link, useNavigate } from "@tanstack/react-router";
import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { useCodeSearch, type CodeSearchResult } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Icon, Input } from "../../ui/index.ts";
import { Gate, RefSwitcher, RepoFrame } from "./common.tsx";

export type CodeSearchParams = { q?: string; ref?: string; path?: string; regexp?: boolean; ignoreCase?: boolean };

/** In-repository code search (git grep on one ref). */
export function SearchPage({ owner, name, params }: { owner: string; name: string; params: CodeSearchParams }) {
  return (
    <RepoFrame owner={owner} name={name} tab="search" head={false}>
      {(repo) => <SearchBody repo={repo} params={params} />}
    </RepoFrame>
  );
}

function SearchBody({ repo, params }: { repo: Repo; params: CodeSearchParams }) {
  const t = useT("code");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const ref = params.ref || repo.defaultBranch;
  const s = { q: params.q ?? "", ref, path: params.path ?? "", regexp: !!params.regexp, ignoreCase: params.ignoreCase ?? true };
  const result = useCodeSearch(owner, name, s);
  const [q, setQ] = useState(s.q);
  const [path, setPath] = useState(s.path);
  const [regexp, setRegexp] = useState(s.regexp);
  const [ignoreCase, setIgnoreCase] = useState(s.ignoreCase);
  const [lang, setLang] = useState<string>();
  const [dir, setDir] = useState<string>();
  const go = (over: Partial<typeof s>) => {
    const n = { ...s, q, path, regexp, ignoreCase, ...over };
    setLang(undefined);
    setDir(undefined);
    void navigate({
      to: "/$owner/$name/search",
      params: { owner, name },
      search: {
        q: n.q.trim() || undefined,
        ref: n.ref === repo.defaultBranch ? undefined : n.ref,
        path: n.path.trim() || undefined,
        regexp: n.regexp || undefined,
        ignoreCase: n.ignoreCase ? undefined : false,
      },
    });
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    go({});
  };
  return (
    <div className="sr-page">
      <form className="sr-q" role="search" onSubmit={submit}>
        <label className="cx-find sr-field">
          <Icon name="search" />
          <input type="search" value={q} placeholder={t("search.query")} aria-label={t("search.query")} onChange={(e) => setQ(e.target.value)} autoFocus />
        </label>
        <Button type="submit" variant="primary" disabled={!q.trim()}>
          {t("search.submit")}
        </Button>
      </form>
      <div className="sr-grid">
        <aside className="sr-side" aria-label={t("search.filters")}>
          <h2>{t("search.ref")}</h2>
          <RefSwitcher owner={owner} name={name} current={ref} onPick={(r) => go({ ref: r })} />
          <h2>{t("search.options")}</h2>
          <Input label={t("search.path")} hint={t("search.pathHint")} mono value={path} onChange={(e) => setPath(e.target.value)} onBlur={() => path !== s.path && go({})} />
          <label className="acc-check">
            <input type="checkbox" checked={regexp} onChange={(e) => { setRegexp(e.target.checked); go({ regexp: e.target.checked }); }} />
            <span>{t("search.regexp")}</span>
          </label>
          <label className="acc-check">
            <input type="checkbox" checked={ignoreCase} onChange={(e) => { setIgnoreCase(e.target.checked); go({ ignoreCase: e.target.checked }); }} />
            <span>{t("search.ignoreCase")}</span>
          </label>
          {result.data && s.q.trim() !== "" && <Facets result={result.data} lang={lang} dir={dir} onLang={setLang} onDir={setDir} />}
        </aside>
        <div className="sr-main">
          {s.q.trim() === "" ? (
            <EmptyState icon="search" title={t("search.emptyTitle")} description={t("search.emptyHint")} />
          ) : (
            <Gate query={result}>{(r) => <Results repo={repo} result={r} search={s} lang={lang} dir={dir} />}</Gate>
          )}
        </div>
      </div>
    </div>
  );
}

const extOf = (path: string) => {
  const base = path.split("/").pop() ?? "";
  const dot = base.lastIndexOf(".");
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : "";
};
const topDir = (path: string) => (path.includes("/") ? path.slice(0, path.indexOf("/") + 1) : "/");

/** Files per extension and per top-level folder among the matches. */
function facets(result: CodeSearchResult) {
  const files = new Set(result.matches.map((m) => m.path));
  const count = (key: (p: string) => string) => {
    const m = new Map<string, number>();
    for (const f of files) m.set(key(f), (m.get(key(f)) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => b[1] - a[1]);
  };
  return { langs: count(extOf), dirs: count(topDir) };
}

/** Left-column filters over the loaded results: file type (extension) and top-level folder. */
function Facets({ result, lang, dir, onLang, onDir }: { result: CodeSearchResult; lang: string | undefined; dir: string | undefined; onLang: (v?: string) => void; onDir: (v?: string) => void }) {
  const t = useT("code");
  const { langs, dirs } = useMemo(() => facets(result), [result]);
  if (langs.length < 2 && dirs.length < 2) return null;
  return (
    <>
      {langs.length > 1 && (
        <>
          <h2>{t("search.languages")}</h2>
          <nav className="sr-facets" aria-label={t("search.languages")}>
            {langs.map(([l, n]) => (
              <button key={l} type="button" aria-pressed={lang === l} onClick={() => onLang(lang === l ? undefined : l)}>
                <span className="ui-mono">{l ? `.${l}` : t("search.noExt")}</span>
                <span className="ui-n">{n}</span>
              </button>
            ))}
          </nav>
        </>
      )}
      {dirs.length > 1 && (
        <>
          <h2>{t("search.paths")}</h2>
          <nav className="sr-facets" aria-label={t("search.paths")}>
            {dirs.map(([d, n]) => (
              <button key={d} type="button" aria-pressed={dir === d} onClick={() => onDir(dir === d ? undefined : d)}>
                <span className="ui-mono">{d}</span>
                <span className="ui-n">{n}</span>
              </button>
            ))}
          </nav>
        </>
      )}
    </>
  );
}

function Results({ repo, result, search, lang, dir }: {
  repo: Repo;
  result: CodeSearchResult;
  search: { q: string; regexp: boolean; ignoreCase: boolean };
  lang?: string;
  dir?: string;
}) {
  const t = useT("code");
  const groups = useMemo(() => {
    const m = new Map<string, CodeSearchResult["matches"]>();
    for (const x of result.matches) {
      if ((lang !== undefined && extOf(x.path) !== lang) || (dir !== undefined && topDir(x.path) !== dir)) continue;
      const g = m.get(x.path);
      if (g) g.push(x);
      else m.set(x.path, [x]);
    }
    return [...m.entries()];
  }, [result, lang, dir]);
  const re = useMemo(() => matcher(search.q, search.regexp, search.ignoreCase), [search.q, search.regexp, search.ignoreCase]);
  if (groups.length === 0) return <EmptyState icon="search" title={t("search.none")} />;
  const matches = groups.reduce((n, [, ms]) => n + ms.length, 0);
  return (
    <>
      <p className="sr-count" role="status">
        <b>{t("search.count", { matches, files: groups.length })}</b>
      </p>
      {groups.map(([path, ms]) => (
        <section key={path} className="ui-box sr-hit" aria-label={path}>
          <div className="ui-box-h">
            <Icon name="file" />
            <Link className="sr-path ui-mono" to="/$owner/$name/blob" params={{ owner: repo.owner, name: repo.name }} search={{ ref: result.ref.name, path }}>
              {path}
            </Link>
            <span className="acc-grow" />
            <span className="ui-tm">{t(ms.length === 1 ? "search.fileMatches.one" : "search.fileMatches.other", { count: ms.length })}</span>
          </div>
          <ul className="rp-hitlist">
            {ms.map((m) => (
              <li key={m.line}>
                <Link
                  to="/$owner/$name/blob"
                  params={{ owner: repo.owner, name: repo.name }}
                  search={{ ref: result.ref.name, path }}
                  hash={`L${m.line}`}
                  className="rp-hit"
                >
                  <span className="cv-ln">{m.line}</span>
                  <code className="rp-hit-tx">{mark(m.text, re)}</code>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ))}
      {result.truncated && <p className="ui-note">{t("search.truncated")}</p>}
    </>
  );
}

function matcher(q: string, regexp: boolean, ignoreCase: boolean): RegExp | undefined {
  try {
    return new RegExp(regexp ? q : q.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), ignoreCase ? "gi" : "g");
  } catch {
    return undefined; // git's regex dialect may differ from JavaScript's: show the line unmarked
  }
}

function mark(text: string, re: RegExp | undefined): ReactNode {
  if (!re) return text;
  const out: ReactNode[] = [];
  let last = 0;
  re.lastIndex = 0;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (m[0] === "") {
      re.lastIndex++;
      continue;
    }
    if (m.index > last) out.push(text.slice(last, m.index));
    out.push(<mark key={m.index}>{m[0]}</mark>);
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}
