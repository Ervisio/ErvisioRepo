import { useEffect, useState } from "react";
import { ApiError } from "../../api/http.ts";
import {
  useHotRepos, usePutHotCache, useTiering,
  type HotRepo, type StorageHotCache, type StorageStatus,
} from "../../api/useStorage.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, EmptyState, Input, SettingRow, Table, TableBody, TableHead, Td, Th, Toggle, Tr } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { formatBytes, Pager, When } from "../code/common.tsx";

const DAY = 86400;
const PAGE = 20;

const days = (seconds: number) => Math.round(seconds / DAY);

/** One repository; asOf is when the list was loaded, to tell an overdue eviction. */
function HotRepoRow({ repo, asOf }: { repo: HotRepo; asOf: number }) {
  const t = useT("admin");
  const notify = useNotify();
  const tiering = useTiering();
  const run = (op: "pin" | "unpin" | "evict") =>
    tiering.mutate({ owner: repo.owner, name: repo.name, op }, { onSuccess: () => notify.ok(t(`storage.repos.${op}Done`)), onError: notify.fail });
  const overdue = repo.evictAt !== undefined && new Date(repo.evictAt).getTime() <= asOf;

  return (
    <Tr>
      <Td className="ui-mono">{repo.fullName}</Td>
      <Td>
        <span className="ui-inline">
          <Badge tone={repo.state === "hot" ? "ok" : repo.state === "degraded" ? "err" : "neutral"}>{t(`storage.repos.state.${repo.state}`)}</Badge>
          {repo.pinned && <Badge tone="info" icon="lock">{t("storage.hot.pinned")}</Badge>}
        </span>
      </Td>
      <Td className="ui-num">{formatBytes(repo.sizeBytes)}</Td>
      <Td><When iso={repo.lastAccessAt} /></Td>
      <Td>
        {repo.evictAt === undefined ? "—" : overdue ? t("storage.hot.evictSoon") : <When iso={repo.evictAt} />}
      </Td>
      <Td>
        <span className="ui-inline">
          {repo.pinned ? (
            <Button size="sm" variant="ghost" disabled={tiering.isPending} onClick={() => run("unpin")}>{t("storage.repos.unpin")}</Button>
          ) : (
            <Button size="sm" disabled={tiering.isPending} title={t("storage.repos.pinTitle")} onClick={() => run("pin")}>{t("storage.repos.pin")}</Button>
          )}
          <Button size="sm" disabled={tiering.isPending || repo.state !== "hot"} title={t("storage.repos.evictTitle")} onClick={() => run("evict")}>{t("storage.repos.evict")}</Button>
        </span>
      </Td>
    </Tr>
  );
}

/** The instance-wide on/off switch and idle TTL of the automatic hot cache; saved changes apply after a restart. */
function HotCacheSettings({ setting }: { setting: StorageHotCache }) {
  const t = useT("admin");
  const notify = useNotify();
  const put = usePutHotCache();
  const [enabled, setEnabled] = useState(setting.enabled);
  const [ttl, setTtl] = useState(setting.ttlSeconds > 0 ? String(days(setting.ttlSeconds)) : "");
  const problems = put.error instanceof ApiError ? (put.error.errors ?? []) : [];
  const ttlError = problems.find((p) => p.path === "ttl")?.msg;
  const n = ttl.trim() === "" ? 0 : Number(ttl);
  const valid = Number.isInteger(n) && n >= 0 && n <= 3650;
  const dirty = enabled !== setting.enabled || n * DAY !== setting.ttlSeconds;
  const d = setting.defaults;

  function save() {
    put.mutate(
      { enabled, ttlSeconds: n * DAY },
      { onSuccess: () => notify.ok(t("storage.saved")), onError: (e) => { if (!(e instanceof ApiError && e.errors?.length)) notify.fail(e); } },
    );
  }

  return (
    <>
      <SettingRow label={t("storage.hot.enabled")} description={t("storage.hot.enabled.hint")}>
        <Toggle label={t("storage.hot.enabled")} checked={enabled} onChange={setEnabled} />
      </SettingRow>
      <SettingRow
        label={t("storage.hot.ttl")}
        description={t("storage.hot.ttl.hint", { git: days(d.git ?? 0), lfs: days(d.lfs ?? 0), actions: days(d.actions ?? 0) })}
      >
        <Input
          label={t("storage.hot.ttl")}
          hideLabel
          type="number"
          inputMode="numeric"
          min={0}
          max={3650}
          placeholder={t("storage.hot.ttl.default")}
          disabled={!enabled}
          value={ttl}
          error={ttlError ?? (valid ? undefined : t("storage.hot.ttl.invalid"))}
          onChange={(e) => setTtl(e.target.value)}
        />
      </SettingRow>
      <div className="acc-actions acc-pad">
        {setting.source === "file" && <span className="ui-note">{t("storage.hot.fromFile")}</span>}
        <Button size="sm" variant="primary" disabled={!dirty || !valid || put.isPending} onClick={save}>{t("storage.target.save")}</Button>
      </div>
    </>
  );
}

/**
 * Hot cache: the automatic cache setting, how much of the local disk it uses
 * and the repositories kept there, with pin, unpin and evict for each one.
 */
export function HotCacheCard({ setting, caches }: { setting: StorageHotCache; caches: StorageStatus["caches"] }) {
  const t = useT("admin");
  const [text, setText] = useState("");
  const [query, setQuery] = useState("");
  const [all, setAll] = useState(false);
  const [page, setPage] = useState(1);
  useEffect(() => {
    const id = setTimeout(() => { setQuery(text.trim()); setPage(1); }, 250);
    return () => clearTimeout(id);
  }, [text]);
  const list = useHotRepos(query, all, (page - 1) * PAGE, PAGE);
  const sum = list.data?.summary;
  const objects = caches.reduce((n, c) => n + c.entries, 0);
  const objectBytes = caches.reduce((n, c) => n + c.used, 0);
  const objectBudget = caches.every((c) => c.budget > 0) ? caches.reduce((n, c) => n + c.budget, 0) : 0;
  const ofBudget = (used: number, budget: number) =>
    budget > 0 ? t("storage.hot.ofBudget", { used: formatBytes(used), budget: formatBytes(budget) }) : formatBytes(used);

  return (
    <Card title={t("storage.hot")}>
      <p className="ui-note acc-pad">{t("storage.hot.hint")}</p>
      <HotCacheSettings key={`${setting.enabled}|${setting.ttlSeconds}`} setting={setting} />
      <dl className="ui-kv">
        {sum && (
          <>
            <div className="ui-kv-row">
              <dt>{t("storage.hot.repos")}</dt>
              <dd>{t("storage.hot.reposValue", { repos: sum.repos, pinned: sum.pinned, size: ofBudget(sum.bytes, sum.budget) })}</dd>
            </div>
            <div className="ui-kv-row">
              <dt>{t("storage.hot.repoTtl")}</dt>
              <dd>{!sum.remote ? t("storage.hot.noRemote") : sum.ttlSeconds > 0 ? t("storage.hot.days", { days: days(sum.ttlSeconds) }) : t("storage.hot.never")}</dd>
            </div>
            {sum.budget > 0 && (
              <div className="ui-kv-row">
                <dt>{t("storage.hot.watermarks")}</dt>
                <dd>{t("storage.hot.watermarksValue", { high: Math.round(sum.highWatermark * 100), low: Math.round(sum.lowWatermark * 100) })}</dd>
              </div>
            )}
          </>
        )}
        <div className="ui-kv-row">
          <dt>{t("storage.hot.objects")}</dt>
          <dd>{caches.length === 0 ? t("storage.hot.objectsNone") : t("storage.hot.objectsValue", { objects, size: ofBudget(objectBytes, objectBudget) })}</dd>
        </div>
      </dl>

      <div className="acc-pad">
        <Input label={t("storage.repos.search")} hint={t("storage.repos.searchHint")} value={text} onChange={(e) => setText(e.target.value)} />
        <label className="acc-check">
          <input type="checkbox" checked={all} onChange={(e) => { setAll(e.target.checked); setPage(1); }} />
          {t("storage.hot.all")}
        </label>
      </div>
      {list.isError && (
        <div role="alert">
          <EmptyState icon="alert" title={t("storage.repos.error")} description={list.error.message} />
        </div>
      )}
      {list.data &&
        (list.data.items.length === 0 ? (
          <EmptyState icon="db" title={query || all ? t("storage.repos.empty") : t("storage.hot.empty")} />
        ) : (
          <Table>
            <TableHead>
              <Tr>
                <Th>{t("storage.repos.col.repo")}</Th>
                <Th>{t("storage.repos.col.state")}</Th>
                <Th className="ui-num">{t("storage.repos.col.size")}</Th>
                <Th>{t("storage.hot.col.used")}</Th>
                <Th>{t("storage.hot.col.evict")}</Th>
                <Th />
              </Tr>
            </TableHead>
            <TableBody>
              {list.data.items.map((r) => <HotRepoRow key={r.id} repo={r} asOf={list.dataUpdatedAt} />)}
            </TableBody>
          </Table>
        ))}
      {list.data && <Pager page={page} hasMore={page * PAGE < list.data.total} onPage={setPage} />}
    </Card>
  );
}
