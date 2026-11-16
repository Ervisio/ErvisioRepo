import { Link, useLocation } from "@tanstack/react-router";
import { useState, type ReactNode } from "react";
import { archiveUrl } from "../../api/code.ts";
import { useSession, useUserProfile } from "../../api/identity.ts";
import { useRepoSubscription, useSetRepoSubscription, type RepoSubscription } from "../../api/notifications.ts";
import { useStar, type Repo } from "../../api/repos.ts";
import { useMeta } from "../../api/useMeta.ts";
import { currentLocale } from "../../i18n/locale.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Icon, Menu, MenuItem, MenuLabel, MenuRadioGroup, MenuRadioItem, MenuSeparator, type IconName } from "../../ui/index.ts";
import { Avatar } from "../../ui/shell/ConsoleShell.tsx";
import { useNotify } from "../account/common.tsx";
import { usePinToggle } from "../repos/PinButton.tsx";
import { ForkDialog } from "../repos/RepoList.tsx";
import { cloneUrls } from "../repos/RepoPage.tsx";

const count = (n: number) => new Intl.NumberFormat(currentLocale(), { notation: "compact" }).format(n);

/** Current location, for the sign-in redirect of the signed-out buttons. */
function useNext() {
  return useLocation({ select: (l) => l.pathname + (l.searchStr ?? "") });
}

/** Signed-out stand-in for an action button: same look, goes to sign-in and back. */
function SignInButton({ icon, label, extra }: { icon: IconName; label: string; extra?: ReactNode }) {
  const next = useNext();
  return (
    <Link to="/login" search={{ next }} className="ui-btn ui-btn-secondary ui-btn-sm rh-btn" title={label}>
      <Icon name={icon} />
      <span className="rh-lbl">{label}</span>
      {extra}
    </Link>
  );
}

/** Repository header (round 012 A): owner avatar, owner / name, visibility, then Pin, Watch, Fork and Star. */
export function RepoHead({ repo }: { repo: Repo }) {
  const tr = useT("repos");
  return (
    <div className="rh">
      <OwnerAvatar repo={repo} />
      <div className="rh-t">
        {repo.ownerKind === "org" ? (
          <Link to="/orgs/$org" params={{ org: repo.owner }}>{repo.owner}</Link>
        ) : (
          <Link to="/$owner" params={{ owner: repo.owner }}>{repo.owner}</Link>
        )}
        <span className="rh-sep" aria-hidden="true">/</span>
        <Link to="/$owner/$name" params={{ owner: repo.owner, name: repo.name }}>
          <b>{repo.name}</b>
        </Link>
      </div>
      <span className="rl-vis">{tr(`visibility.${repo.visibility}`)}</span>
      <div className="rh-act">
        <PinAction repo={repo} />
        <WatchAction repo={repo} />
        <ForkAction repo={repo} />
        <StarButton repo={repo} />
      </div>
    </div>
  );
}

function OwnerAvatar({ repo }: { repo: Repo }) {
  if (repo.ownerKind === "org") {
    return <span className="rh-av rh-org" aria-hidden="true">{repo.owner.slice(0, 1).toUpperCase()}</span>;
  }
  return <UserAvatar username={repo.owner} />;
}

function UserAvatar({ username }: { username: string }) {
  const p = useUserProfile(username);
  return (
    <span className="rh-av" aria-hidden="true">
      <Avatar user={{ username, avatarUrl: p.data?.avatarUrl ?? "" }} />
    </span>
  );
}

function PinAction({ repo }: { repo: Repo }) {
  const pin = usePinToggle(repo.owner, repo.name);
  if (!pin) return null;
  return (
    <Button size="sm" icon="pin" className="rh-btn" aria-pressed={pin.pinned} title={pin.label} disabled={pin.pending} onClick={pin.toggle}>
      <span className="rh-lbl">{pin.label}</span>
    </Button>
  );
}

const LEVELS: RepoSubscription["level"][] = ["watching", "participating", "ignoring"];

/** Watch = repository notification level; the API has no watcher count, so none is shown. */
function WatchAction({ repo }: { repo: Repo }) {
  const t = useT("code");
  const tw = useT("work");
  const notify = useNotify();
  const signedIn = useSession().data !== undefined;
  const sub = useRepoSubscription(repo.owner, repo.name, signedIn);
  const set = useSetRepoSubscription(repo.owner, repo.name);
  if (!signedIn) return <SignInButton icon="eye" label={t("head.watch")} />;
  const level = sub.data?.level ?? "participating";
  const label = level === "watching" ? t("head.unwatch") : level === "ignoring" ? t("head.ignoring") : t("head.watch");
  return (
    <Menu
      align="end"
      label={tw("side.watchLabel")}
      trigger={
        <Button size="sm" icon="eye" className="rh-btn" title={label} disabled={!sub.data}>
          <span className="rh-lbl">{label}</span>
          <Icon name="down" />
        </Button>
      }
    >
      <MenuLabel>{tw("side.watchLabel")}</MenuLabel>
      {LEVELS.map((l) => (
        <MenuItem key={l} icon={l === level ? "check" : undefined} onSelect={() => set.mutate(l, { onError: notify.fail })}>
          {tw(`watch.${l}`)}
        </MenuItem>
      ))}
    </Menu>
  );
}

/** Fork; the API reports no fork count, so the button carries none. */
function ForkAction({ repo }: { repo: Repo }) {
  const tr = useT("repos");
  const signedIn = useSession().data !== undefined;
  const [open, setOpen] = useState(false);
  if (!signedIn) return <SignInButton icon="fork" label={tr("fork.button")} />;
  return (
    <>
      <Button size="sm" icon="fork" className="rh-btn" title={tr("fork.button")} onClick={() => setOpen(true)}>
        <span className="rh-lbl">{tr("fork.button")}</span>
      </Button>
      <ForkDialog repo={repo} open={open} onOpenChange={setOpen} />
    </>
  );
}

/** Star toggle with the star count. */
export function StarButton({ repo }: { repo: Repo }) {
  const t = useT("code");
  const notify = useNotify();
  const signedIn = useSession().data !== undefined;
  const star = useStar(repo.owner, repo.name);
  const n = <span className="rh-cnt" aria-label={t("head.starsLabel", { count: repo.starsCount })}>{count(repo.starsCount)}</span>;
  if (!signedIn) return <SignInButton icon="star" label={t("head.star")} extra={n} />;
  const label = repo.starred ? t("head.starred") : t("head.star");
  return (
    <Button
      size="sm"
      className={repo.starred ? "rh-btn rh-starred" : "rh-btn"}
      aria-pressed={repo.starred}
      title={label}
      disabled={star.isPending}
      onClick={() => star.mutate(!repo.starred, { onError: notify.fail })}
    >
      <Icon name="star" />
      <span className="rh-lbl">{label}</span>
      {n}
    </Button>
  );
}

type CloneKind = "https" | "ssh" | "cli";

/** GitHub-style "Code" button: clone URLs (HTTPS, SSH, CLI) and source archives in one drop-down card. */
export function CloneMenu({ repo, refName }: { repo: Repo; refName: string }) {
  const t = useT("code");
  const ta = useT("account");
  const notify = useNotify();
  const meta = useMeta();
  const urls = cloneUrls(meta.data?.publicUrl ?? window.location.origin, repo.owner, repo.name);
  const [kind, setKind] = useState<CloneKind>("https");
  const kinds: CloneKind[] = urls.ssh ? ["https", "ssh", "cli"] : ["https", "cli"];
  const value = kind === "ssh" ? urls.ssh : kind === "cli" ? `eg repo clone ${repo.owner}/${repo.name}` : urls.http;
  const copy = () => navigator.clipboard.writeText(value).then(() => notify.ok(ta("copied")), (e: unknown) => notify.fail(e));
  return (
    <Menu
      align="end"
      className="rh-clone"
      label={t("clone.title")}
      trigger={
        <Button variant="primary" icon="code">
          {t("clone.button")}
          <Icon name="down" />
        </Button>
      }
    >
      <div className="rh-cb">
        <p className="rh-ct">
          <Icon name="terminal" />
          {t("clone.title")}
        </p>
        <MenuRadioGroup value={kind} onValueChange={(v) => setKind(v as CloneKind)} label={t("clone.protocol")} className="rh-seg">
          {kinds.map((k) => (
            <MenuRadioItem key={k} value={k} className="rh-segi">
              {t(`clone.${k}`)}
            </MenuRadioItem>
          ))}
        </MenuRadioGroup>
        <div className="rh-url">
          <code>{value}</code>
          <MenuItem
            className="rh-copy"
            onSelect={(e) => {
              e.preventDefault();
              void copy();
            }}
          >
            <Icon name="copy" />
            <span className="sr-only">{ta("copy")}</span>
          </MenuItem>
        </div>
        <p className="rh-hint">{t(`clone.hint.${kind}`)}</p>
      </div>
      <MenuSeparator />
      {(["zip", "tar.gz"] as const).map((f) => (
        <MenuItem key={f} asChild>
          <a href={archiveUrl(repo.owner, repo.name, refName, f)} download>
            <Icon name="download" />
            {t(`clone.${f === "zip" ? "zip" : "tgz"}`)}
          </a>
        </MenuItem>
      ))}
    </Menu>
  );
}
