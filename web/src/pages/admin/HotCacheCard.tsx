import { useEffect, useState } from "react";
import { ApiError } from "../../api/http.ts";
import {
  useHotRepos, usePutHotCache,
  type HotRepo, type StorageHotCache, type StorageStatus,
} from "../../api/useStorage.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, EmptyState, Input, SettingRow, Table, TableBody, TableHead, Td, Th, Toggle, Tr } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { formatBytes, Pager, When } from "../code/common.tsx";

const DAY = 86400;
const GIB = 1024 ** 3;
const PAGE = 20;

const days = (seconds: number) => Math.round(seconds / DAY);
const gib = (bytes: number) => String(Math.round((bytes / GIB) * 10) / 10);

/**
 * Turns what the admin typed into an owner/name query: a repository URL
 * (https://host/owner/name[.git][/...], or git@host:owner/name.git) becomes
 * owner/name, anything else stays a substring search.
 */
function repoQuery(text: string): string {
  const s = text.trim();
  const ssh = /^[\w.-]+@[\w.-]+:([^/\s]+)\/([^/\s]+?)(?:\.git)?\/?$/.exec(s);
  if (ssh) return `${ssh[1]}/${ssh[2]}`;
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) {
    try {
      const [owner, name] = new URL(s).pathname.split("/").filter(Boolean);
      if (owner && name) return `${owner}/${name.replace(/\.git$/, "")}`;
    } catch {
      return s;
    }
  }
  return s;
}

/** Where one repository is stored right now; asOf is when the list was loaded, to tell an overdue eviction. */
function RepoLocationRow({ repo, asOf, primary, replicas }: { repo: HotRepo; asOf: number; primary: string; replicas: string[] }) {
  const t = useT("admin");
  const overdue = repo.evictAt !== undefined && new Date(repo.evictAt).getTime() <= asOf;
  const local = repo.state === "hot" || repo.state === "evicting";

  return (
    <Tr>
      <Td className="ui-mono">{repo.fullName}</Td>
      <Td>
        <span className="ui-inline">
          <Badge tone={repo.state === "hot" ? "ok" : repo.state === "degraded" ? "err" : "neutral"}>{t(`storage.repos.state.${repo.state}`)}</Badge>
          {repo.ephemeral && <Badge tone="info">{t("storage.repos.ephemeral")}</Badge>}
        </span>
      </Td>
      <Td className="ui-num">{formatBytes(repo.totalSizeBytes)}</Td>
      <Td>
        {primary === ""
          ? t("storage.repos.where.localOnly")
          : local
            ? t("storage.repos.where.local", { primary })
            : primary}
        {replicas.length > 0 && <div className="ui-note">{t("storage.repos.where.replicas", { replicas: replicas.join(", ") })}</div>}
      </Td>
      <Td><When iso={repo.lastAccessAt} /></Td>
      <Td>
        {repo.evictAt === undefined ? "—" : overdue ? t("storage.hot.evictSoon") : <When iso={repo.evictAt} />}
      </Td>
    </Tr>
  );
}

/** The instance-wide switch, idle TTL and size limits of the automatic hot cache; saved changes apply after a restart. */
function HotCacheSettings({ setting }: { setting: StorageHotCache }) {
  const t = useT("admin");
  const notify = useNotify();
  const put = usePutHotCache();
  const [enabled, setEnabled] = useState(setting.enabled);
  const [ttl, setTtl] = useState(setting.ttlSeconds > 0 ? String(days(setting.ttlSeconds)) : "");
  const [maxSize, setMaxSize] = useState(setting.maxBytes > 0 ? gib(setting.maxBytes) : "");
  const [large, setLarge] = useState(setting.largeRepoBytes > 0 ? gib(setting.largeRepoBytes) : "");
  const problems = put.error instanceof ApiError ? (put.error.errors ?? []) : [];
  const ttlError = problems.find((p) => p.path === "ttl")?.msg;
  const maxError = problems.find((p) => p.path === "maxBytes")?.msg;
  const largeError = problems.find((p) => p.path === "largeRepoBytes")?.msg;
  const n = ttl.trim() === "" ? 0 : Number(ttl);
  const toBytes = (v: string) => (v.trim() === "" ? 0 : Math.round(Number(v) * GIB));
  const maxBytes = toBytes(maxSize);
  const largeBytes = toBytes(large);
  const validTtl = Number.isInteger(n) && n >= 0 && n <= 3650;
  const validMax = Number.isFinite(maxBytes) && (maxBytes === 0 || maxBytes >= GIB);
  const validLarge = Number.isFinite(largeBytes) && largeBytes >= 0;
  const valid = validTtl && validMax && validLarge;
  const dirty = enabled !== setting.enabled || n * DAY !== setting.ttlSeconds ||
    maxBytes !== setting.maxBytes || largeBytes !== setting.largeRepoBytes;
  const d = setting.defaults;

  function save() {
    put.mutate(
      { enabled, ttlSeconds: n * DAY, maxBytes, largeRepoBytes: largeBytes },
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
          error={ttlError ?? (validTtl ? undefined : t("storage.hot.ttl.invalid"))}
          onChange={(e) => setTtl(e.target.value)}
        />
      </SettingRow>
      <SettingRow label={t("storage.hot.maxSize")} description={t("storage.hot.maxSize.hint", { size: gib(setting.defaultMaxBytes) })}>
        <Input
          label={t("storage.hot.maxSize")}
          hideLabel
          type="number"
          inputMode="decimal"
          min={1}
          step="any"
          placeholder={gib(setting.defaultMaxBytes)}
          value={maxSize}
          error={maxError ?? (validMax ? undefined : t("storage.hot.maxSize.invalid"))}
          onChange={(e) => setMaxSize(e.target.value)}
        />
      </SettingRow>
      <SettingRow label={t("storage.hot.largeRepo")} description={t("storage.hot.largeRepo.hint", { size: gib(setting.defaultLargeRepoBytes) })}>
        <Input
          label={t("storage.hot.largeRepo")}
          hideLabel
          type="number"
          inputMode="decimal"
          min={0}
          step="any"
          placeholder={gib(setting.defaultLargeRepoBytes)}
          disabled={!enabled}
          value={large}
          error={largeError ?? (validLarge ? undefined : t("storage.hot.largeRepo.invalid"))}
          onChange={(e) => setLarge(e.target.value)}
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
 * and a lookup of where a repository is stored right now. Placement is
 * automatic: there is no per-repository pin or evict.
 */
export function HotCacheCard({ setting, caches }: { setting: StorageHotCache; caches: StorageStatus["caches"] }) {
  const t = useT("admin");
  const [text, setText] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  useEffect(() => {
    const id = setTimeout(() => { setQuery(repoQuery(text)); setPage(1); }, 250);
    return () => clearTimeout(id);
  }, [text]);
  const list = useHotRepos(query, true, (page - 1) * PAGE, PAGE);
  const sum = list.data?.summary;
  const objects = caches.reduce((n, c) => n + c.entries, 0);
  const objectBytes = caches.reduce((n, c) => n + c.used, 0);
  const objectBudget = caches.every((c) => c.budget > 0) ? caches.reduce((n, c) => n + c.budget, 0) : 0;
  const ofBudget = (used: number, budget: number) =>
    budget > 0 ? t("storage.hot.ofBudget", { used: formatBytes(used), budget: formatBytes(budget) }) : formatBytes(used);
  const searching = query !== "";

  return (
    <Card title={t("storage.hot")}>
      <p className="ui-note acc-pad">{t("storage.hot.hint")}</p>
      <HotCacheSettings key={`${setting.enabled}|${setting.ttlSeconds}|${setting.maxBytes}|${setting.largeRepoBytes}`} setting={setting} />
      <dl className="ui-kv">
        {sum && (
          <>
            <div className="ui-kv-row">
              <dt title={t("storage.hot.totalGit.hint")}>{t("storage.hot.totalGit")}</dt>
              <dd>{formatBytes(sum.totalGitBytes)}</dd>
            </div>
            <div className="ui-kv-row">
              <dt>{t("storage.hot.repos")}</dt>
              <dd>{t("storage.hot.reposValue", { repos: sum.repos, size: formatBytes(sum.bytes) })}</dd>
            </div>
            <div className="ui-kv-row">
              <dt>{t("storage.hot.repoTtl")}</dt>
              <dd>{!sum.remote ? t("storage.hot.noRemote") : sum.ttlSeconds > 0 ? t("storage.hot.days", { days: days(sum.ttlSeconds) }) : t("storage.hot.never")}</dd>
            </div>
            {sum.remote && (
              <div className="ui-kv-row">
                <dt>{t("storage.hot.ephemeral")}</dt>
                <dd>{sum.cacheEnabled ? t("storage.hot.ephemeralValue", { size: formatBytes(sum.largeRepoBytes) }) : t("storage.hot.ephemeralAll")}</dd>
              </div>
            )}
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
      </div>
      {searching && list.isError && (
        <div role="alert">
          <EmptyState icon="alert" title={t("storage.repos.error")} description={list.error.message} />
        </div>
      )}
      {searching && list.data &&
        (list.data.items.length === 0 ? (
          <EmptyState icon="db" title={t("storage.repos.empty")} />
        ) : (
          <Table>
            <TableHead>
              <Tr>
                <Th>{t("storage.repos.col.repo")}</Th>
                <Th>{t("storage.repos.col.state")}</Th>
                <Th className="ui-num">{t("storage.repos.col.size")}</Th>
                <Th>{t("storage.repos.col.where")}</Th>
                <Th>{t("storage.hot.col.used")}</Th>
                <Th>{t("storage.hot.col.evict")}</Th>
              </Tr>
            </TableHead>
            <TableBody>
              {list.data.items.map((r) => (
                <RepoLocationRow key={r.id} repo={r} asOf={list.dataUpdatedAt}
                  primary={list.data.summary.primary} replicas={list.data.summary.replicas} />
              ))}
            </TableBody>
          </Table>
        ))}
      {searching && list.data && <Pager page={page} hasMore={page * PAGE < list.data.total} onPage={setPage} />}
    </Card>
  );
}
