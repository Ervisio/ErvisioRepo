import { useNavigate } from "@tanstack/react-router";
import { useCompare } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, EmptyState, Icon } from "../../ui/index.ts";
import { DiffView } from "../../ui/code/DiffView.tsx";
import { Gate, RefSwitcher, RepoFrame } from "./common.tsx";
import { CommitRow } from "./HistoryPages.tsx";

/** Commits and diff between two refs (three-dot: changes on head since the merge base). */
export function ComparePage({ owner, name, base, head }: { owner: string; name: string; base?: string; head?: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="branches">
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
    <>
      <div className="rp-bar" role="group" aria-label={t("compare.label")}>
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
            <>
              <Card title={t("compare.title", { base: c.base.name, head: c.head.name })}>
                <p className="ui-note acc-pad">{t("compare.summary", { ahead: c.ahead, behind: c.behind })}</p>
                {c.commits.length === 0 ? (
                  <p className="ui-note acc-pad">{t("compare.identical")}</p>
                ) : (
                  <ul className="rp-list">
                    {c.commits.map((x) => (
                      <CommitRow key={x.sha} repo={repo} c={{ sha: x.sha, summary: x.summary, author: x.author.name, date: x.author.date }} />
                    ))}
                  </ul>
                )}
                {c.commitsTruncated && <p className="ui-note acc-pad">{t("compare.commitsTruncated")}</p>}
              </Card>
              <DiffView diff={c.diff} />
            </>
          )}
        </Gate>
      )}
    </>
  );
}
