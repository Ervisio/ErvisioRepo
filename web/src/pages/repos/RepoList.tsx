import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent, type ReactNode } from "react";
import { useMyOrgs, useSession } from "../../api/identity.ts";
import { useWeeklyActivity } from "../../api/profile.ts";
import { useCreateRepo, useForkRepo, useStar, type Repo, type Visibility } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, Icon, Input, Select } from "../../ui/index.ts";
import { RepoImage } from "../../ui/AvatarImg.tsx";
import { formatBytes, When } from "../code/common.tsx";
import { PinButton } from "./PinButton.tsx";
import { Async, useNotify } from "../account/common.tsx";
import type { UseQueryResult } from "@tanstack/react-query";

export const visibilities: Visibility[] = ["private", "internal", "public"];

type TypeFilter = "all" | Visibility;
type SortKey = "updated" | "name" | "stars";

/** Main language of a repository, by full name; only some pages know it (the organization page). */
export type RepoLanguages = Map<string, { language: string; color: string }>;

/** Language dot and name. */
export function LangTag({ language, color }: { language: string; color: string }) {
  return (
    <span className="rl-lang">
      <i style={{ background: color }} aria-hidden="true" />
      {language}
    </span>
  );
}

/** Star toggle of a repository row; nothing while signed out. */
export function StarButton({ repo }: { repo: Repo }) {
  const t = useT("repos");
  const signedIn = !!useSession().data;
  const star = useStar(repo.owner, repo.name);
  if (!signedIn) return null;
  return (
    <Button size="sm" icon="star" className={repo.starred ? "rl-starred" : undefined} aria-pressed={repo.starred} disabled={star.isPending} onClick={() => star.mutate(!repo.starred)}>
      {repo.starred ? t("star.starred") : t("star.star")}
    </Button>
  );
}

/** Activity of the last weeks as a line; flat when nothing happened. */
export function Sparkline({ weeks }: { weeks: number[] }) {
  const t = useT("repos");
  if (weeks.length < 2) return null;
  const max = Math.max(1, ...weeks);
  const step = 150 / (weeks.length - 1);
  const d = weeks.map((v, i) => `${i === 0 ? "M" : "L"}${(i * step).toFixed(1)} ${(25 - (v / max) * 22).toFixed(1)}`).join(" ");
  const total = weeks.reduce((a, b) => a + b, 0);
  return (
    <svg className="rl-spark" viewBox="0 0 150 28" preserveAspectRatio="none" role="img" aria-label={t("list.activity", { count: total, weeks: weeks.length })}>
      <path d={d} fill="none" stroke="currentColor" strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

/** One repository in GitHub style: name, visibility, description, language, stars, last update; star, pin and activity on the side. */
export function RepoRow({ repo: r, showOwner, pin = true, lang, weeks }: { repo: Repo; showOwner?: boolean; pin?: boolean; lang?: { language: string; color: string }; weeks?: number[] }) {
  const t = useT("repos");
  return (
    <li className="rl-row">
      <div className="rl-main">
        <h3 className="rl-h">
          <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="rl-name">
            <RepoImage src={r.avatarUrl} />
            {showOwner && <span className="rl-o">{r.owner} / </span>}
            {r.name}
          </Link>
          <span className="rl-vis">{t(`visibility.${r.visibility}`)}</span>
        </h3>
        {r.description && <p className="rl-desc">{r.description}</p>}
        <p className="rl-meta">
          {lang && <LangTag {...lang} />}
          {r.starsCount > 0 && (
            <span className="rl-stat" title={t("list.stars", { count: r.starsCount })}>
              <Icon name="star" />
              {r.starsCount}
            </span>
          )}
          <span>{t("list.updated")} <When iso={r.updatedAt} /></span>
          <span title={t("size.total.hint")}>{t("size.totalValue", { size: formatBytes(r.totalSizeBytes) })}</span>
        </p>
      </div>
      <div className="rl-side">
        <div className="rl-act">
          {pin && <PinButton owner={r.owner} name={r.name} compact />}
          <StarButton repo={r} />
        </div>
        {weeks && <Sparkline weeks={weeks} />}
      </div>
    </li>
  );
}

/** Repository cards in two columns: pinned or popular repositories of a profile or organization. */
export function RepoCards({ repos, showOwner, languages }: { repos: Repo[]; showOwner?: boolean; languages?: RepoLanguages }) {
  const t = useT("repos");
  return (
    <ul className="rl-pins">
      {repos.map((r) => {
        const lang = languages?.get(r.fullName);
        return (
          <li key={r.id} className="rl-pin">
            <p className="rl-pin-n">
              {r.avatarUrl ? <RepoImage src={r.avatarUrl} size={16} /> : <Icon name="book" />}
              <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="rl-name">
                {showOwner ? r.fullName : r.name}
              </Link>
              <span className="rl-vis">{t(`visibility.${r.visibility}`)}</span>
            </p>
            <p className="rl-pin-d">{r.description}</p>
            <p className="rl-meta">
              {lang && <LangTag {...lang} />}
              {r.starsCount > 0 && <span className="rl-stat"><Icon name="star" />{r.starsCount}</span>}
            </p>
          </li>
        );
      })}
    </ul>
  );
}

/** The most starred repositories, then the latest updated: what a profile shows when nothing is pinned. */
export const popular = (repos: Repo[], n: number) =>
  [...repos].filter((r) => r.visibility === "public").sort((a, b) => b.starsCount - a.starsCount || b.updatedAt.localeCompare(a.updatedAt)).slice(0, n);

/** Repository list with GitHub's filter bar: find by name, type, language and sort, all on the loaded page. */
export function RepoRows({ query, showOwner, action, languages, pin = true }: { query: UseQueryResult<Repo[]>; showOwner?: boolean; action?: ReactNode; languages?: RepoLanguages; pin?: boolean }) {
  const t = useT("repos");
  const [q, setQ] = useState("");
  const [type, setType] = useState<TypeFilter>("all");
  const [lang, setLang] = useState("");
  const [sort, setSort] = useState<SortKey>("updated");
  const needle = q.trim().toLowerCase();
  const langNames = languages ? [...new Set([...languages.values()].map((l) => l.language))].sort() : [];
  const shown = (query.data ?? [])
    .filter((r) => (type === "all" || r.visibility === type) && (!lang || languages?.get(r.fullName)?.language === lang) && (!needle || `${r.name} ${r.description}`.toLowerCase().includes(needle)))
    .sort((x, y) => (sort === "name" ? x.name.localeCompare(y.name) : sort === "stars" ? y.starsCount - x.starsCount || y.updatedAt.localeCompare(x.updatedAt) : y.updatedAt.localeCompare(x.updatedAt)));
  const activity = useWeeklyActivity(shown.slice(0, 100).map((r) => r.id));
  return (
    <div className="rl">
      <div className="rl-bar" role="search">
        <Input className="rl-find" label={t("list.find")} hideLabel type="search" placeholder={t("list.find")} value={q} onChange={(e) => setQ(e.target.value)} />
        <Select
          label={t("list.type")}
          hideLabel
          value={type}
          onValueChange={(v) => setType(v as TypeFilter)}
          options={[{ value: "all", label: t("list.type.all") }, ...visibilities.map((v) => ({ value: v, label: t(`visibility.${v}`) }))]}
        />
        {langNames.length > 0 && (
          <Select
            label={t("list.language")}
            hideLabel
            value={lang || "all"}
            onValueChange={(v) => setLang(v === "all" ? "" : v)}
            options={[{ value: "all", label: t("list.language.all") }, ...langNames.map((l) => ({ value: l, label: l }))]}
          />
        )}
        <Select
          label={t("explore.sort")}
          hideLabel
          value={sort}
          onValueChange={(v) => setSort(v as SortKey)}
          options={[{ value: "updated", label: t("explore.sort.updated") }, { value: "name", label: t("explore.sort.name") }, { value: "stars", label: t("explore.sort.stars") }]}
        />
        {action}
      </div>
      <Async query={query} empty={t("list.empty")}>
        {() =>
          shown.length === 0 ? (
            <p className="ui-note rl-none">{t("explore.noMatch")}</p>
          ) : (
            <ul className="rl-list">
              {shown.map((r) => <RepoRow key={r.id} repo={r} showOwner={showOwner} pin={pin} lang={languages?.get(r.fullName)} weeks={activity.data?.get(r.id)} />)}
            </ul>
          )
        }
      </Async>
    </div>
  );
}

/** Dialog with the create form; with `org` the repository is created under that organization. */
/** New-repository dialog. Pass `open`/`onOpenChange` to control it from outside (the shell's New menu): then no trigger is rendered. */
export function CreateRepoDialog({ org, open: openProp, onOpenChange }: { org?: string; open?: boolean; onOpenChange?: (open: boolean) => void }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateRepo(org);
  const [openState, setOpenState] = useState(false);
  const controlled = openProp !== undefined;
  const open = controlled ? openProp : openState;
  const setOpen = (o: boolean) => (controlled ? onOpenChange?.(o) : setOpenState(o));
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [visibility, setVisibility] = useState<Visibility>("private");
  const [readme, setReadme] = useState(false);

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      { name: name.trim(), description: description.trim(), visibility, autoInit: readme },
      {
        onSuccess: (r) => {
          setOpen(false);
          setName("");
          setDescription("");
          setReadme(false);
          notify.ok(t("create.done"));
          void navigate({ to: "/$owner/$name", params: { owner: r.owner, name: r.name } });
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen} title={t("create.title")} description={t("create.description")} trigger={controlled ? undefined : <Button variant="primary" icon="plus">{t("create.button")}</Button>}>
      <form className="acc-form" onSubmit={submit}>
        <Input label={t("create.name")} required maxLength={100} autoComplete="off" mono value={name} onChange={(e) => setName(e.target.value)} />
        <Input label={t("create.descriptionLabel")} maxLength={350} value={description} onChange={(e) => setDescription(e.target.value)} />
        <Select label={t("create.visibility")} value={visibility} onValueChange={(v) => setVisibility(v as Visibility)} options={visibilities.map((v) => ({ value: v, label: t(`visibility.${v}`) }))} />
        <p className="ui-note">{t(`visibility.${visibility}.hint`)}</p>
        <label className="acc-check">
          <input type="checkbox" checked={readme} onChange={(e) => setReadme(e.target.checked)} />
          <span>{t("create.readme")}</span>
        </label>
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={create.isPending}>{t("create.submit")}</Button>
        </div>
      </form>
    </Dialog>
  );
}

/** Fork dialog: the copy goes to the signed-in user or to an organization they manage. */
export function ForkDialog({ repo, open: openProp, onOpenChange }: { repo: Repo; open?: boolean; onOpenChange?: (open: boolean) => void }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const fork = useForkRepo(repo.owner, repo.name);
  const user = useSession().data;
  const orgs = useMyOrgs().data ?? [];
  const [openState, setOpenState] = useState(false);
  // Controlled when the caller opens it from a menu; it then renders no trigger.
  const controlled = openProp !== undefined;
  const open = controlled ? openProp : openState;
  const setOpen = (v: boolean) => (controlled ? onOpenChange?.(v) : setOpenState(v));
  const [owner, setOwner] = useState(user?.username ?? "");
  const [name, setName] = useState(repo.name);
  // The server checks the permission; this only narrows the choice.
  const owners = [user?.username ?? "", ...orgs.filter((o) => o.role === "owner" || o.role === "admin").map((o) => o.name)].filter(Boolean);

  function submit(e: FormEvent) {
    e.preventDefault();
    fork.mutate(
      { owner, name: name.trim() },
      {
        onSuccess: (r) => {
          setOpen(false);
          notify.ok(t("fork.done"));
          void navigate({ to: "/$owner/$name", params: { owner: r.owner, name: r.name } });
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen} title={t("fork.title", { name: repo.fullName })} description={t("fork.description")} trigger={controlled ? undefined : <Button icon="branch">{t("fork.button")}</Button>}>
      <form className="acc-form" onSubmit={submit}>
        <Select label={t("fork.owner")} value={owner} onValueChange={setOwner} options={owners.map((o) => ({ value: o, label: o }))} />
        <Input label={t("create.name")} required maxLength={100} autoComplete="off" mono value={name} onChange={(e) => setName(e.target.value)} />
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={fork.isPending || owner === "" || name.trim() === ""}>{t("fork.submit")}</Button>
        </div>
      </form>
    </Dialog>
  );
}
