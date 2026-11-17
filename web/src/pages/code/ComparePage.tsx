import { Link, useNavigate } from "@tanstack/react-router";
import { canWrite, useCompare } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Icon } from "../../ui/index.ts";
import { DiffView } from "../../ui/code/DiffView.tsx";
import { Gate, RefSwitcher, RepoFrame } from "./common.tsx";
import { CommitRow } from "./HistoryPages.tsx";

/** Commits and diff between two refs (three-dot: changes on head since the merge base). */
export function ComparePage({ owner, name, base, head }: { owner: string; name: string; base?: string; head?: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="branches" head={false}>
      {(repo) => <CompareBody repo={repo} base={base || repo.defaultBranch} head={head ?? ""} />}
    </RepoFrame>
  );
}

function CompareBody({ repo, base, head }: { repo: Repo; base: string; head: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const cmp = useCompare(owner, name, base, head);
  const go = (b: string, h: string) => void navigate({ to: "/$owner/$name/compare", params: { owner, name }, search: { base: b, head: h || undefined } });
  return (
    <div className="cd-page">
      <div className="rp-bar cd-refs" role="group" aria-label={t("compare.label")}>
        <span className="ui-note">{t("compare.base")}</span>
        <RefSwitcher owner={owner} name={name} current={base} onPick={(r) => go(r, head)} />
        <Icon name="right" />
        <span className="ui-note">{t("compare.head")}</span>
        <RefSwitcher owner={owner} name={name} current={head || t("compare.pick")} onPick={(r) => go(base, r)} />
        <Button icon="layers" disabled={!head} onClick={() => go(head, base)}>
          {t("compare.swap")}
        </Button>
      </div>
      {!head ? (
        <EmptyState icon="branch" title={t("compare.pickTitle")} description={t("compare.pickHint")} />
      ) : (
        <Gate query={cmp} notFound={<EmptyState icon="branch" title={t("compare.notFound")} />}>
          {(c) => (
            <DiffView
              diff={c.diff}
              header={
                <section className="cd-cmp" aria-label={t("compare.title", { base: c.base.name, head: c.head.name })}>
                  <div className="cm-top">
                    <h1>{t("compare.title", { base: c.base.name, head: c.head.name })}</h1>
                    <span className="ui-note">{t("compare.summary", { ahead: c.ahead, behind: c.behind })}</span>
                    <span className="acc-grow" />
                    {canWrite(repo.permission) && c.ahead > 0 && c.head.kind === "branch" && (
                      <Link className="ui-btn ui-btn-primary" to="/$owner/$name/pulls" params={{ owner, name }} search={{ compare: true }}>
                        <Icon name="pr" />
                        {t("compare.openPull")}
                      </Link>
                    )}
                  </div>
                  {c.commits.length === 0 ? (
                    <p className="ui-note acc-pad">{t("compare.identical")}</p>
                  ) : (
                    <ul className="rp-list">
                      {c.commits.map((x) => (
                        <CommitRow key={x.sha} repo={repo} c={{ sha: x.sha, summary: x.summary, message: x.message, author: x.author.name, user: x.author.user, date: x.author.date }} />
                      ))}
                    </ul>
                  )}
                  {c.commitsTruncated && <p className="ui-note acc-pad">{t("compare.commitsTruncated")}</p>}
                </section>
              }
            />
          )}
        </Gate>
      )}
    </div>
  );
}
