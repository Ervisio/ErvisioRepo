import { Link, useNavigate } from "@tanstack/react-router";
import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { useCodeSearch, type CodeSearchResult } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, EmptyState, Input } from "../../ui/index.ts";
import { Gate, RefSwitcher, RepoFrame } from "./common.tsx";

export type CodeSearchParams = { q?: string; ref?: string; path?: string; regexp?: boolean; ignoreCase?: boolean };

/** In-repository code search (git grep on one ref). */
export function SearchPage({ owner, name, params }: { owner: string; name: string; params: CodeSearchParams }) {
  return (
    <RepoFrame owner={owner} name={name} tab="search">
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
  const go = (over: Partial<typeof s>) => {
    const n = { ...s, q, path, regexp, ignoreCase, ...over };
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
    <>
      <Card>
        <form className="acc-form acc-pad rp-search" role="search" onSubmit={submit}>
          <div className="rp-bar">
            <RefSwitcher owner={owner} name={name} current={ref} onPick={(r) => go({ ref: r })} />
            <div className="acc-grow">
              <Input label={t("search.query")} mono value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
            </div>
          </div>
          <Input label={t("search.path")} hint={t("search.pathHint")} mono value={path} onChange={(e) => setPath(e.target.value)} />
          <label className="acc-check">
            <input type="checkbox" checked={regexp} onChange={(e) => setRegexp(e.target.checked)} />
            <span>{t("search.regexp")}</span>
          </label>
          <label className="acc-check">
            <input type="checkbox" checked={ignoreCase} onChange={(e) => setIgnoreCase(e.target.checked)} />
            <span>{t("search.ignoreCase")}</span>
          </label>
          <div className="acc-actions">
            <Button type="submit" variant="primary" icon="search" disabled={!q.trim()}>
              {t("search.submit")}
            </Button>
          </div>
        </form>
      </Card>
      {s.q.trim() === "" ? (
        <EmptyState icon="search" title={t("search.emptyTitle")} description={t("search.emptyHint")} />
      ) : (
        <Gate query={result}>{(r) => <Results repo={repo} result={r} search={s} />}</Gate>
      )}
    </>
  );
}

function Results({ repo, result, search }: { repo: Repo; result: CodeSearchResult; search: { q: string; regexp: boolean; ignoreCase: boolean } }) {
  const t = useT("code");
  const groups = useMemo(() => {
    const m = new Map<string, CodeSearchResult["matches"]>();
    for (const x of result.matches) {
      const g = m.get(x.path);
      if (g) g.push(x);
      else m.set(x.path, [x]);
    }
    return [...m.entries()];
  }, [result]);
  const re = useMemo(() => matcher(search.q, search.regexp, search.ignoreCase), [search.q, search.regexp, search.ignoreCase]);
  if (groups.length === 0) return <EmptyState icon="search" title={t("search.none")} />;
  return (
    <>
      <p className="ui-note" role="status">
        {t("search.count", { matches: result.matches.length, files: groups.length })}
      </p>
      {groups.map(([path, ms]) => (
        <Card key={path} className="rp-hits">
          <div className="rp-fh">
            <Link className="ui-mono" to="/$owner/$name/blob" params={{ owner: repo.owner, name: repo.name }} search={{ ref: result.ref.name, path }}>
              {path}
            </Link>
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
        </Card>
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
