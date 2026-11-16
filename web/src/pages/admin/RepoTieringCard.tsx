import { useEffect, useState } from "react";
import type { Repo } from "../../api/repos.ts";
import { useRepoSearch, useTiering } from "../../api/useStorage.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, EmptyState, Input, Table, TableBody, TableHead, Td, Th, Tr } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { formatBytes } from "../code/common.tsx";

function RepoRow({ repo }: { repo: Repo }) {
  const t = useT("admin");
  const notify = useNotify();
  const tiering = useTiering();
  const state = tiering.data?.state ?? repo.storageState;
  const run = (op: "pin" | "unpin" | "evict") =>
    tiering.mutate({ owner: repo.owner, name: repo.name, op }, { onSuccess: () => notify.ok(t(`storage.repos.${op}Done`)), onError: notify.fail });

  return (
    <Tr>
      <Td className="ui-mono">{repo.fullName}</Td>
      <Td>
        <span className="ui-inline">
          <Badge tone={state === "hot" ? "ok" : state === "degraded" ? "err" : "neutral"}>{t(`storage.repos.state.${state}`)}</Badge>
          {tiering.data && <span className="ui-note">{tiering.data.pinned ? t("storage.repos.pinned") : t("storage.repos.unpinned")}</span>}
        </span>
      </Td>
      <Td className="ui-num">{formatBytes(tiering.data?.sizeBytes ?? repo.sizeBytes)}</Td>
      <Td>
        <span className="ui-inline">
          <Button size="sm" disabled={tiering.isPending} title={t("storage.repos.pinTitle")} onClick={() => run("pin")}>{t("storage.repos.pin")}</Button>
          <Button size="sm" variant="ghost" disabled={tiering.isPending} onClick={() => run("unpin")}>{t("storage.repos.unpin")}</Button>
          <Button size="sm" disabled={tiering.isPending || state !== "hot"} title={t("storage.repos.evictTitle")} onClick={() => run("evict")}>{t("storage.repos.evict")}</Button>
        </span>
      </Td>
    </Tr>
  );
}

/** Per-repository hot-tier controls: search a repository, then pin it on the hot tier, unpin it or evict it now. */
export function RepoTieringCard() {
  const t = useT("admin");
  const [text, setText] = useState("");
  const [query, setQuery] = useState("");
  useEffect(() => {
    const id = setTimeout(() => setQuery(text.trim()), 250);
    return () => clearTimeout(id);
  }, [text]);
  const found = useRepoSearch(query);

  return (
    <Card title={t("storage.repos")}>
      <p className="ui-note acc-pad">{t("storage.repos.hint")}</p>
      <div className="acc-pad">
        <Input label={t("storage.repos.search")} hint={t("storage.repos.searchHint")} value={text} onChange={(e) => setText(e.target.value)} />
      </div>
      {found.isError && (
        <div role="alert">
          <EmptyState icon="alert" title={t("storage.repos.error")} description={found.error.message} />
        </div>
      )}
      {found.data &&
        (found.data.length === 0 ? (
          <EmptyState icon="db" title={t("storage.repos.empty")} />
        ) : (
          <Table>
            <TableHead>
              <Tr>
                <Th>{t("storage.repos.col.repo")}</Th>
                <Th>{t("storage.repos.col.state")}</Th>
                <Th className="ui-num">{t("storage.repos.col.size")}</Th>
                <Th />
              </Tr>
            </TableHead>
            <TableBody>
              {found.data.map((r) => (
                <RepoRow key={r.id} repo={r} />
              ))}
            </TableBody>
          </Table>
        ))}
    </Card>
  );
}
