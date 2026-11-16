import { useId, useMemo, useState } from "react";
import {
  eventAllowed,
  levelOf,
  type AppCatalog,
  type AppPermissions,
  type PermissionLevel,
  type RepositorySelection,
} from "../../api/apps.ts";
import { useQuery } from "@tanstack/react-query";
import { request } from "../../api/http.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Input } from "../../ui/index.ts";

const groups = ["repository", "organization", "user"] as const;
const levels: PermissionLevel[] = ["none", "read", "write"];

/** Human label and hint of a permission; unknown names are shown as they are. */
export function usePermText() {
  const t = useT("apps");
  return {
    label: (p: string) => {
      const k = `perm.${p}`;
      const v = t(k);
      return v === k ? p.replace(/_/g, " ") : v;
    },
    hint: (p: string) => {
      const k = `perm.${p}.hint`;
      const v = t(k);
      return v === k ? "" : v;
    },
  };
}

/** Sets one permission level; "none" removes the key. */
function withLevel(perms: AppPermissions, name: string, level: PermissionLevel): AppPermissions {
  const next = { ...perms };
  if (level === "none") delete next[name];
  else next[name] = level;
  return next;
}

/**
 * Permission matrix: a bordered box per group, one row per permission with a None / Read / Write
 * segmented control. Metadata is fixed at Read; write-only permissions offer None and Write.
 * Lowering a permission drops the events it no longer allows.
 */
export function PermissionMatrix({ catalog, perms, events, onChange }: {
  catalog: AppCatalog;
  perms: AppPermissions;
  events: string[];
  onChange: (perms: AppPermissions, events: string[]) => void;
}) {
  const t = useT("apps");
  const text = usePermText();
  const uid = useId();
  function set(name: string, level: PermissionLevel) {
    const next = withLevel(perms, name, level);
    const allowed = new Set(catalog.events.filter((ev) => eventAllowed(next, ev)).map((ev) => ev.name));
    onChange(next, events.filter((e) => allowed.has(e)));
  }
  return (
    <div className="ap-groups">
      {groups.map((g) => catalog[g].length > 0 && (
        <fieldset key={g} className="ap-perms">
          <legend className="ap-perms-h">{t(`perm.group.${g}`)}</legend>
          {catalog[g].map((p) => {
            const fixed = p === "metadata";
            const writeOnly = catalog.writeOnly.includes(p);
            const cur = levelOf(perms, p);
            return (
              <div key={p} className="ap-perm">
                <div className="ap-perm-m">
                  <span className="ap-perm-l">{text.label(p)}</span>
                  <span className="ui-note">{fixed ? t("perm.alwaysRead") : text.hint(p)}</span>
                </div>
                <div className="ap-seg" role="radiogroup" aria-label={text.label(p)}>
                  {levels.map((l) => {
                    const disabled = fixed ? l !== "read" : writeOnly && l === "read";
                    return (
                      <label key={l} className="ap-segi" data-on={cur === l || undefined} data-off={disabled || undefined}>
                        <input
                          type="radio"
                          className="sr-only"
                          name={`${uid}-${p}`}
                          value={l}
                          checked={cur === l}
                          disabled={disabled}
                          onChange={() => set(p, l)}
                        />
                        {t(`level.${l}`)}
                      </label>
                    );
                  })}
                </div>
              </div>
            );
          })}
        </fieldset>
      ))}
    </div>
  );
}

/** Webhook events as chips; only those covered by the permissions can be picked. */
export function EventChips({ catalog, perms, events, onChange }: {
  catalog: AppCatalog;
  perms: AppPermissions;
  events: string[];
  onChange: (events: string[]) => void;
}) {
  const t = useT("apps");
  const text = usePermText();
  const toggle = (e: string) => onChange(events.includes(e) ? events.filter((x) => x !== e) : [...events, e]);
  return (
    <div className="ap-chips" role="group" aria-label={t("events.title")}>
      {catalog.events.map((ev) => {
        const ok = eventAllowed(perms, ev);
        const need = [ev.permission, ev.alternative].filter((x): x is string => !!x).map(text.label).join(" / ");
        return (
          <button
            key={ev.name}
            type="button"
            className="ap-chip"
            aria-pressed={events.includes(ev.name)}
            disabled={!ok}
            title={ok ? undefined : t("events.needs", { perm: need })}
            onClick={() => toggle(ev.name)}
          >
            {events.includes(ev.name) && <span aria-hidden="true">✓ </span>}
            {ev.name}
          </button>
        );
      })}
    </div>
  );
}

/** Permissions in words: write access, read access, then the events. */
export function PermissionSummary({ perms, events }: { perms: AppPermissions | undefined; events: string[] }) {
  const t = useT("apps");
  const text = usePermText();
  const names = Object.keys(perms ?? {});
  if (!names.includes("metadata")) names.push("metadata");
  const write = names.filter((p) => levelOf(perms, p) === "write").map(text.label);
  const read = names.filter((p) => levelOf(perms, p) === "read").map(text.label);
  return (
    <ul className="oa-consent-s">
      {write.length > 0 && <li>{t("summary.write", { list: write.join(", ") })}</li>}
      {read.length > 0 && <li>{t("summary.read", { list: read.join(", ") })}</li>}
      <li>{events.length > 0 ? t("summary.events", { list: events.join(", ") }) : t("summary.noEvents")}</li>
    </ul>
  );
}

/** Rows that differ between the accepted and the requested permissions. */
export function PermissionDiff({ current, requested, events, requestedEvents }: {
  current: AppPermissions;
  requested: AppPermissions;
  events: string[];
  requestedEvents: string[];
}) {
  const t = useT("apps");
  const text = usePermText();
  const names = [...new Set([...Object.keys(current), ...Object.keys(requested)])].filter((p) => levelOf(current, p) !== levelOf(requested, p));
  const added = requestedEvents.filter((e) => !events.includes(e));
  const removed = events.filter((e) => !requestedEvents.includes(e));
  return (
    <div className="ap-diff">
      {names.length > 0 && (
        <table className="ap-diff-t">
          <thead>
            <tr><th>{t("pending.perm")}</th><th>{t("pending.current")}</th><th>{t("pending.requested")}</th></tr>
          </thead>
          <tbody>
            {names.map((p) => (
              <tr key={p}>
                <td>{text.label(p)}</td>
                <td>{t(`level.${levelOf(current, p)}`)}</td>
                <td><b>{t(`level.${levelOf(requested, p)}`)}</b></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {added.length > 0 && <p className="ui-note">{t("pending.eventsAdded", { list: added.join(", ") })}</p>}
      {removed.length > 0 && <p className="ui-note">{t("pending.eventsRemoved", { list: removed.join(", ") })}</p>}
    </div>
  );
}

/** Square with the initial: installations carry no logo. */
export function Initial({ name, size = "md" }: { name: string; size?: "md" | "lg" }) {
  return <span className={`oa-logo oa-logo-${size}`} aria-hidden="true">{(name.trim()[0] ?? "?").toUpperCase()}</span>;
}

/** Repository names of an account: the caller's own repositories or an organization's. */
function useAccountRepoNames(account: { kind: "user" | "org"; name: string }) {
  // Same keys as useMyRepos / useOwnerRepos, so the caches are shared.
  const q = useQuery({
    queryKey: account.kind === "org" ? ["repos", "org", account.name] : ["repos", "mine"],
    queryFn: () => request<Repo[]>("GET", account.kind === "org" ? `/orgs/${encodeURIComponent(account.name)}/repos?limit=100` : "/user/repos?limit=100"),
  });
  const names = useMemo(
    () => (q.data ?? []).filter((r) => r.owner === account.name).map((r) => r.name).sort((a, b) => a.localeCompare(b)),
    [q.data, account.name],
  );
  return { names, loading: q.isPending, error: q.isError };
}

/** All repositories, or a chosen set picked by name from the account's repositories. */
export function RepoSelection({ account, selection, repos, onChange }: {
  account: { kind: "user" | "org"; name: string };
  selection: RepositorySelection;
  repos: string[];
  onChange: (selection: RepositorySelection, repos: string[]) => void;
}) {
  const t = useT("apps");
  const tc = useT("common");
  const uid = useId();
  const [filter, setFilter] = useState("");
  const list = useAccountRepoNames(account);
  const shown = list.names.filter((n) => !repos.includes(n) && n.toLowerCase().includes(filter.trim().toLowerCase())).slice(0, 8);
  return (
    <div className="ap-repos">
      <label className="ap-radio">
        <input type="radio" name={`${uid}-sel`} checked={selection === "all"} onChange={() => onChange("all", repos)} />
        <span><b>{t("repos.all")}</b><span className="ui-note">{t("repos.allHint")}</span></span>
      </label>
      <label className="ap-radio">
        <input type="radio" name={`${uid}-sel`} checked={selection === "selected"} onChange={() => onChange("selected", repos)} />
        <span><b>{t("repos.selected")}</b><span className="ui-note">{t("repos.selectedHint")}</span></span>
      </label>
      {selection === "selected" && (
        <div className="ap-pick">
          {repos.length > 0 && (
            <ul className="ap-picked" aria-label={t("repos.picked")}>
              {repos.map((r) => (
                <li key={r} className="ap-chip" data-on>
                  <span className="ui-mono">{r}</span>
                  <Button size="sm" variant="ghost" icon="x" aria-label={t("repos.remove", { name: r })} onClick={() => onChange("selected", repos.filter((x) => x !== r))} />
                </li>
              ))}
            </ul>
          )}
          <Input label={t("repos.search")} hideLabel placeholder={t("repos.search")} value={filter} onChange={(e) => setFilter(e.target.value)} />
          {list.loading ? (
            <p role="status" className="ui-note">{tc("state.loading")}</p>
          ) : list.error ? (
            <p role="alert" className="ui-note">{t("repos.loadError")}</p>
          ) : shown.length === 0 ? (
            <p className="ui-note">{t("repos.none")}</p>
          ) : (
            <ul className="ap-options">
              {shown.map((n) => (
                <li key={n}>
                  <button type="button" className="ap-option" onClick={() => { onChange("selected", [...repos, n]); setFilter(""); }}>
                    <span className="ui-mono">{account.name}/{n}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
