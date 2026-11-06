import type { UseQueryResult } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { archiveUrl, isHydrating, useBranches, useTags } from "../../api/code.ts";
import { ApiError } from "../../api/http.ts";
import { useRepo, type Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, EmptyState, Icon, Menu, MenuItem, MenuLabel, MenuSeparator, Page } from "../../ui/index.ts";
import { useLayout } from "../../ui/shell/layout.ts";
import type { Section } from "../../ui/shell/nav.tsx";
import { useProblemText } from "../auth/useProblemText.ts";
import { PinButton } from "../repos/PinButton.tsx";
import { ForkDialog } from "../repos/RepoList.tsx";

export type RepoTab = "code" | "issues" | "pulls" | "commits" | "branches" | "tags" | "releases" | "actions" | "search";

export const shortSha = (sha: string) => sha.slice(0, 7);

/** Repository pages: title, actions, sub-navigation and the repo query itself. */
export function RepoFrame({ owner, name, tab, section = "code", actions, children }: {
  owner: string;
  name: string;
  tab: RepoTab;
  section?: Section;
  actions?: ReactNode;
  children: (repo: Repo) => ReactNode;
}) {
  const t = useT("code");
  const tr = useT("repos");
  const repo = useRepo(owner, name);
  const layout = useLayout();
  const title = `${owner}/${name}`;
  if (repo.isPending || repo.isError) {
    return (
      <Page section={section} title={title}>
        <Gate query={repo}>{() => null}</Gate>
      </Page>
    );
  }
  const r = repo.data;
  const tabs = [
    { id: "code", to: "/$owner/$name", icon: "code" },
    { id: "issues", to: "/$owner/$name/issues", icon: "issue" },
    { id: "pulls", to: "/$owner/$name/pulls", icon: "pr" },
    { id: "commits", to: "/$owner/$name/commits", icon: "commit" },
    { id: "branches", to: "/$owner/$name/branches", icon: "branch" },
    { id: "tags", to: "/$owner/$name/tags", icon: "tag" },
    { id: "actions", to: "/$owner/$name/actions", icon: "play" },
    { id: "releases", to: "/$owner/$name/releases", icon: "pkg" },
    { id: "search", to: "/$owner/$name/search", icon: "search" },
  ] as const;
  return (
    <Page
      section={section}
      title={r.fullName}
      subtitle={r.description || undefined}
      actions={
        <>
          <Badge tone={r.visibility === "public" ? "ok" : "neutral"}>{tr(`visibility.${r.visibility}`)}</Badge>
          {actions}
          <PinButton owner={r.owner} name={r.name} />
          <ForkDialog repo={r} />
          {r.permission === "admin" && (
            <Link to="/$owner/$name/settings" params={{ owner: r.owner, name: r.name }}>
              <Button icon="gear">{tr("settings.link")}</Button>
            </Link>
          )}
        </>
      }
    >
      {/* The console sidebar already lists these pages. */}
      {layout !== "console" && (
        <nav className="ui-tablist rp-nav" aria-label={t("nav.label")}>
          {tabs.map((x) => (
            <Link
              key={x.id}
              to={x.to}
              params={{ owner: r.owner, name: r.name }}
              className="ui-tab"
              data-state={x.id === tab ? "active" : "inactive"}
              aria-current={x.id === tab ? "page" : undefined}
            >
              <Icon name={x.icon} />
              {t(`nav.${x.id}`)}
            </Link>
          ))}
        </nav>
      )}
      {children(r)}
    </Page>
  );
}

/** Loading, "repository is being prepared", error and not-found states around a query. */
export function Gate<T>({ query, notFound, children }: {
  query: UseQueryResult<T>;
  notFound?: ReactNode;
  children: (data: T) => ReactNode;
}) {
  const t = useT("code");
  const tc = useT("common");
  const problem = useProblemText();
  if (query.isPending) {
    if (query.fetchStatus === "idle") return null;
    return (
      <p role="status" className="ui-note acc-pad">
        {isHydrating(query.failureReason) ? t("state.hydrating") : tc("state.loading")}
      </p>
    );
  }
  if (query.isError) {
    if (notFound !== undefined && query.error instanceof ApiError && query.error.status === 404) return <>{notFound}</>;
    return (
      <div role="alert">
        <EmptyState
          icon="alert"
          title={isHydrating(query.error) ? t("state.hydratingSlow") : t("state.error")}
          description={problem(query.error)}
          action={<Button onClick={() => void query.refetch()}>{tc("action.retry")}</Button>}
        />
      </div>
    );
  }
  return <>{children(query.data)}</>;
}

const MENU_REFS = 10;

/** Branch/tag picker. `onPick` receives the chosen ref name. */
export function RefSwitcher({ owner, name, current, onPick }: { owner: string; name: string; current: string; onPick: (ref: string) => void }) {
  const t = useT("code");
  const branches = useBranches(owner, name, 0, MENU_REFS);
  const tags = useTags(owner, name, 0, MENU_REFS);
  const label = current.length === 40 ? shortSha(current) : current;
  return (
    <Menu
      trigger={
        <Button icon="branch" aria-label={t("ref.switch", { ref: label })}>
          <span className="rp-ref">{label}</span>
          <Icon name="down" />
        </Button>
      }
    >
      <MenuLabel>{t("ref.branches")}</MenuLabel>
      {branches.data?.items.map((b) => (
        <MenuItem key={`b-${b.name}`} icon={b.name === current ? "check" : "branch"} onSelect={() => onPick(b.name)}>
          {b.name}
        </MenuItem>
      ))}
      {branches.data?.hasMore && (
        <MenuItem asChild>
          <Link to="/$owner/$name/branches" params={{ owner, name }}>
            {t("ref.allBranches")}
          </Link>
        </MenuItem>
      )}
      <MenuSeparator />
      <MenuLabel>{t("ref.tags")}</MenuLabel>
      {tags.data?.items.length === 0 && <MenuLabel>{t("ref.noTags")}</MenuLabel>}
      {tags.data?.items.map((g) => (
        <MenuItem key={`t-${g.name}`} icon={g.name === current ? "check" : "tag"} onSelect={() => onPick(g.name)}>
          {g.name}
        </MenuItem>
      ))}
      {tags.data?.hasMore && (
        <MenuItem asChild>
          <Link to="/$owner/$name/tags" params={{ owner, name }}>
            {t("ref.allTags")}
          </Link>
        </MenuItem>
      )}
    </Menu>
  );
}

/** Source archive download (zip / tar.gz) of a ref. */
export function ArchiveMenu({ owner, name, refName }: { owner: string; name: string; refName: string }) {
  const t = useT("code");
  return (
    <Menu
      align="end"
      trigger={
        <Button icon="download">
          {t("archive.label")}
          <Icon name="down" />
        </Button>
      }
    >
      {(["zip", "tar.gz"] as const).map((f) => (
        <MenuItem key={f} asChild>
          <a href={archiveUrl(owner, name, refName, f)} download>
            <Icon name="download" />
            {t(`archive.${f === "zip" ? "zip" : "tgz"}`)}
          </a>
        </MenuItem>
      ))}
    </Menu>
  );
}

/** Breadcrumb path: repository root, then each directory, the last segment as plain text. */
export function Crumb({ owner, name, refName, path }: { owner: string; name: string; refName: string; path: string }) {
  const t = useT("code");
  const parts = path.split("/").filter(Boolean);
  return (
    <nav className="rp-crumb" aria-label={t("crumb.label")}>
      {parts.length === 0 ? (
        <span>{name}</span>
      ) : (
        <Link to="/$owner/$name" params={{ owner, name }} search={{ ref: refName }}>
          {name}
        </Link>
      )}
      {parts.map((p, i) => {
        const sub = parts.slice(0, i + 1).join("/");
        return (
          <span key={sub} className="rp-crumb-seg">
            <span className="rp-crumb-sep" aria-hidden="true">
              /
            </span>
            {i === parts.length - 1 ? (
              <span aria-current="page">{p}</span>
            ) : (
              <Link to="/$owner/$name" params={{ owner, name }} search={{ ref: refName, path: sub }}>
                {p}
              </Link>
            )}
          </span>
        );
      })}
    </nav>
  );
}

/** Previous / next buttons for offset pagination. `page` is 1-based. */
export function Pager({ page, hasMore, onPage }: { page: number; hasMore: boolean; onPage: (page: number) => void }) {
  const t = useT("code");
  if (page <= 1 && !hasMore) return null;
  return (
    <div className="acc-actions rp-pager">
      <Button size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)}>
        {t("pager.prev")}
      </Button>
      <span className="ui-note">{t("pager.page", { page })}</span>
      <Button size="sm" disabled={!hasMore} onClick={() => onPage(page + 1)}>
        {t("pager.next")}
      </Button>
    </div>
  );
}

/** Relative time ("3 days ago") with the absolute date as a tooltip. */
export function When({ iso }: { iso: string }) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return <span className="rp-when">{iso}</span>;
  return (
    <time className="rp-when" dateTime={iso} title={d.toLocaleString()}>
      {relative(d)}
    </time>
  );
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const steps: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 86400],
  ["month", 30 * 86400],
  ["week", 7 * 86400],
  ["day", 86400],
  ["hour", 3600],
  ["minute", 60],
];

function relative(d: Date): string {
  const s = (d.getTime() - Date.now()) / 1000;
  for (const [unit, secs] of steps) if (Math.abs(s) >= secs) return rtf.format(Math.round(s / secs), unit);
  return rtf.format(Math.round(s), "second");
}

/** Size in B / KiB / MiB / GiB. */
export function formatBytes(n: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
}

/** Parent directory of a path ("" for top-level entries). */
export const dirOf = (path: string) => path.split("/").slice(0, -1).join("/");
