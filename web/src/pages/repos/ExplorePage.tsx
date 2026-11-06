import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useExploreRepos } from "../../api/personal.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, EmptyState, Icon, Input, Page, Select } from "../../ui/index.ts";
import { Pager, When } from "../code/common.tsx";

export type ExploreSearch = { q?: string; sort?: "name"; page?: number };

const PER_PAGE = 20;

/** Public repositories; reachable signed out. Filters live in the URL. */
export function ExplorePage({ search }: { search: ExploreSearch }) {
  const t = useT("repos");
  const tc = useT("common");
  const navigate = useNavigate();
  const page = search.page ?? 1;
  const sort = search.sort ?? "updated";
  const [draft, setDraft] = useState(search.q ?? "");
  const list = useExploreRepos({ q: search.q, sort, page, perPage: PER_PAGE });
  const go = (next: ExploreSearch) => void navigate({ to: "/explore", search: next });

  function submit(e: FormEvent) {
    e.preventDefault();
    go({ q: draft.trim() || undefined, sort: search.sort });
  }

  return (
    <Page section="code" title={t("explore.title")} subtitle={t("explore.subtitle")}>
      <Card>
        <form className="acc-wrap hm-pad" role="search" onSubmit={submit}>
          <Input className="acc-grow" label={t("explore.search")} hideLabel type="search" placeholder={t("explore.searchHint")} value={draft} onChange={(e) => setDraft(e.target.value)} />
          <Select
            label={t("explore.sort")}
            hideLabel
            value={sort}
            onValueChange={(v) => go({ q: search.q, sort: v === "name" ? "name" : undefined })}
            options={[{ value: "updated", label: t("explore.sort.updated") }, { value: "name", label: t("explore.sort.name") }]}
          />
          <Button type="submit" icon="search">{t("explore.search")}</Button>
        </form>
        {list.isPending ? (
          <p role="status" className="ui-note acc-pad">{tc("state.loading")}</p>
        ) : list.isError ? (
          <div role="alert">
            <EmptyState icon="alert" title={list.error.message} action={<Button onClick={() => void list.refetch()}>{tc("action.retry")}</Button>} />
          </div>
        ) : list.data.items.length === 0 ? (
          <p className="ui-note acc-pad">{search.q ? t("explore.noMatch") : t("explore.empty")}</p>
        ) : (
          list.data.items.map((r) => (
            <div key={r.id} className="ui-row acc-wrap">
              <div className="acc-grow">
                <div className="acc-title">
                  <Icon name="book" />
                  <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }}>
                    <b>{r.fullName}</b>
                  </Link>
                  <Badge tone="ok">{t(`visibility.${r.visibility}`)}</Badge>
                </div>
                {r.description && <div className="ui-note">{r.description}</div>}
              </div>
              <When iso={r.updatedAt} />
            </div>
          ))
        )}
      </Card>
      {list.data && <Pager page={page} hasMore={page * PER_PAGE < list.data.totalCount} onPage={(n) => go({ ...search, page: n > 1 ? n : undefined })} />}
    </Page>
  );
}
