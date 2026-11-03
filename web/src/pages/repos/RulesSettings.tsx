import type { UseQueryResult } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { useDeleteRule, useRules, useSaveRule, type BranchProtection, type BranchProtectionInput, type TagRule, type TagRuleInput } from "../../api/settings.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Dialog, DialogClose, Input, Select } from "../../ui/index.ts";
import { Async, useNotify } from "../account/common.tsx";

/** Splits a comma or newline separated list typed by the user. */
export const splitList = (s: string) =>
  s
    .split(/[,\n]/)
    .map((x) => x.trim())
    .filter(Boolean);

function Check({ label, hint, checked, onChange }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="acc-check wk-rcheck">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <span>
        {label}
        {hint && <span className="ui-note wk-rhint">{hint}</span>}
      </span>
    </label>
  );
}

/** Shared list of rules with an editor dialog for new and existing entries. */
function RuleList<R extends { id: string; pattern: string }>({
  title,
  hint,
  rows,
  empty,
  summary,
  editor,
  onDelete,
}: {
  title: string;
  hint: string;
  rows: UseQueryResult<R[]>;
  empty: string;
  summary: (r: R) => ReactNode;
  editor: (r: R | undefined, close: () => void) => ReactNode;
  onDelete: (r: R) => void;
}) {
  const t = useT("protection");
  const [editing, setEditing] = useState<R | "new" | undefined>(undefined);
  const close = () => setEditing(undefined);
  return (
    <Card
      title={title}
      actions={
        <Button variant="primary" icon="plus" onClick={() => setEditing("new")}>
          {t("add")}
        </Button>
      }
    >
      <p className="ui-note acc-pad">{hint}</p>
      <Async query={rows} empty={empty}>
        {(list) => (
          <ul className="rp-list wk-rules">
            {list.map((r) => (
              <li key={r.id} className="wk-rule">
                <span className="ui-mono wk-rule-pat">{r.pattern}</span>
                <span className="wk-rule-sum">{summary(r)}</span>
                <span className="acc-grow" />
                <Button size="sm" icon="edit" onClick={() => setEditing(r)}>
                  {t("edit")}
                </Button>
                <Button size="sm" variant="ghost" icon="trash" aria-label={t("deleteOf", { pattern: r.pattern })} onClick={() => window.confirm(t("deleteConfirm", { pattern: r.pattern })) && onDelete(r)} />
              </li>
            ))}
          </ul>
        )}
      </Async>
      <Dialog
        open={editing !== undefined}
        onOpenChange={(o) => !o && close()}
        title={editing === "new" ? t("newTitle") : t("editTitle", { pattern: editing?.pattern ?? "" })}
        description={t("patternHint")}
      >
        {editing !== undefined && editor(editing === "new" ? undefined : editing, close)}
      </Dialog>
    </Card>
  );
}

export function BranchProtections({ owner, name }: { owner: string; name: string }) {
  const t = useT("protection");
  const notify = useNotify();
  const rows = useRules<BranchProtection>(owner, name, "branch-protections");
  const remove = useDeleteRule(owner, name, "branch-protections");
  return (
    <RuleList
      title={t("branch.title")}
      hint={t("branch.hint")}
      rows={rows}
      empty={t("branch.empty")}
      summary={(r) => (
        <>
          {r.requiredApprovals > 0 && <Badge tone="info">{t("branch.approvalsN", { count: r.requiredApprovals })}</Badge>}
          {r.requiredStatusChecks.length > 0 && <Badge tone="info">{t("branch.checksN", { count: r.requiredStatusChecks.length })}</Badge>}
          {r.requireCodeOwnerReviews && <Badge>{t("branch.owners")}</Badge>}
          {r.restrictPushes && <Badge tone="warn">{t("branch.restricted")}</Badge>}
          {r.allowForcePushes && <Badge tone="err">{t("branch.force")}</Badge>}
        </>
      )}
      editor={(r, close) => <BranchEditor owner={owner} name={name} rule={r} close={close} />}
      onDelete={(r) => remove.mutate(r.id, { onSuccess: () => notify.ok(t("deleted")), onError: notify.fail })}
    />
  );
}

function BranchEditor({ owner, name, rule, close }: { owner: string; name: string; rule?: BranchProtection; close: () => void }) {
  const t = useT("protection");
  const notify = useNotify();
  const save = useSaveRule<BranchProtectionInput>(owner, name, "branch-protections");
  const [v, setV] = useState<BranchProtectionInput>({
    pattern: rule?.pattern ?? "",
    requiredApprovals: rule?.requiredApprovals ?? 1,
    requireCodeOwnerReviews: rule?.requireCodeOwnerReviews ?? false,
    dismissStaleReviews: rule?.dismissStaleReviews ?? false,
    requiredStatusChecks: rule?.requiredStatusChecks ?? [],
    strictStatusChecks: rule?.strictStatusChecks ?? false,
    requireLinearHistory: rule?.requireLinearHistory ?? false,
    allowForcePushes: rule?.allowForcePushes ?? false,
    allowDeletions: rule?.allowDeletions ?? false,
    requireSignedCommits: rule?.requireSignedCommits ?? false,
    restrictPushes: rule?.restrictPushes ?? false,
    pushUsers: rule?.pushUsers ?? [],
    pushTeams: rule?.pushTeams ?? [],
    adminsBypass: rule?.adminsBypass ?? false,
  });
  const [checks, setChecks] = useState((rule?.requiredStatusChecks ?? []).join(", "));
  const [users, setUsers] = useState((rule?.pushUsers ?? []).join(", "));
  const [teams, setTeams] = useState((rule?.pushTeams ?? []).join(", "));
  const set = (patch: Partial<BranchProtectionInput>) => setV((x) => ({ ...x, ...patch }));
  const flag = (k: keyof BranchProtectionInput, hint = true) => (
    <Check label={t(`branch.f.${k}`)} hint={hint ? t(`branch.f.${k}.hint`) : undefined} checked={Boolean(v[k])} onChange={(b) => set({ [k]: b })} />
  );

  function submit(e: FormEvent) {
    e.preventDefault();
    const body = { ...v, pattern: v.pattern.trim(), requiredStatusChecks: splitList(checks), pushUsers: splitList(users), pushTeams: splitList(teams) };
    save.mutate({ id: rule?.id, body }, { onSuccess: () => (notify.ok(t("saved")), close()), onError: notify.fail });
  }

  return (
    <form className="acc-form" onSubmit={submit}>
      <Input label={t("pattern")} required mono autoComplete="off" placeholder="main" value={v.pattern} onChange={(e) => set({ pattern: e.target.value })} />
      <fieldset className="wk-fieldset">
        <legend>{t("branch.reviews")}</legend>
        <Input
          label={t("branch.f.requiredApprovals")}
          type="number"
          min={0}
          max={10}
          value={String(v.requiredApprovals ?? 0)}
          onChange={(e) => set({ requiredApprovals: Math.max(0, Number(e.target.value) || 0) })}
        />
        {flag("requireCodeOwnerReviews")}
        {flag("dismissStaleReviews")}
      </fieldset>
      <fieldset className="wk-fieldset">
        <legend>{t("branch.checks")}</legend>
        <Input label={t("branch.f.requiredStatusChecks")} hint={t("listHint")} mono value={checks} onChange={(e) => setChecks(e.target.value)} />
        {flag("strictStatusChecks")}
      </fieldset>
      <fieldset className="wk-fieldset">
        <legend>{t("branch.history")}</legend>
        {flag("requireLinearHistory")}
        {flag("requireSignedCommits")}
        {flag("allowForcePushes")}
        {flag("allowDeletions")}
      </fieldset>
      <fieldset className="wk-fieldset">
        <legend>{t("branch.push")}</legend>
        {flag("restrictPushes")}
        {v.restrictPushes && (
          <>
            <Input label={t("users")} hint={t("listHint")} mono value={users} onChange={(e) => setUsers(e.target.value)} />
            <Input label={t("teams")} hint={t("listHint")} mono value={teams} onChange={(e) => setTeams(e.target.value)} />
          </>
        )}
        {flag("adminsBypass")}
      </fieldset>
      <div className="acc-actions">
        <DialogClose asChild>
          <Button variant="ghost">{t("cancel")}</Button>
        </DialogClose>
        <Button variant="primary" type="submit" disabled={save.isPending || !v.pattern.trim()}>
          {t("save")}
        </Button>
      </div>
    </form>
  );
}

const levels = ["write", "maintain", "admin", "none"] as const;

export function TagRules({ owner, name }: { owner: string; name: string }) {
  const t = useT("protection");
  const notify = useNotify();
  const rows = useRules<TagRule>(owner, name, "tag-rules");
  const remove = useDeleteRule(owner, name, "tag-rules");
  return (
    <RuleList
      title={t("tag.title")}
      hint={t("tag.hint")}
      rows={rows}
      empty={t("tag.empty")}
      summary={(r) => (
        <>
          {(["create", "update", "delete"] as const).map((a) => (
            <Badge key={a} tone={r[a] === "none" ? "err" : "neutral"}>
              {t(`tag.sum.${a}`, { level: t(`level.${r[a]}`) })}
            </Badge>
          ))}
        </>
      )}
      editor={(r, close) => <TagEditor owner={owner} name={name} rule={r} close={close} />}
      onDelete={(r) => remove.mutate(r.id, { onSuccess: () => notify.ok(t("deleted")), onError: notify.fail })}
    />
  );
}

function TagEditor({ owner, name, rule, close }: { owner: string; name: string; rule?: TagRule; close: () => void }) {
  const t = useT("protection");
  const notify = useNotify();
  const save = useSaveRule<TagRuleInput>(owner, name, "tag-rules");
  const [pattern, setPattern] = useState(rule?.pattern ?? "");
  const [lv, setLv] = useState({ create: rule?.create ?? "maintain", update: rule?.update ?? "none", delete: rule?.delete ?? "admin" });
  const [users, setUsers] = useState((rule?.allowedUsers ?? []).join(", "));
  const [teams, setTeams] = useState((rule?.allowedTeams ?? []).join(", "));
  const [bypass, setBypass] = useState(rule?.adminsBypass ?? false);
  const options = levels.map((l) => ({ value: l, label: t(`level.${l}`) }));

  function submit(e: FormEvent) {
    e.preventDefault();
    const body: TagRuleInput = { pattern: pattern.trim(), ...lv, allowedUsers: splitList(users), allowedTeams: splitList(teams), adminsBypass: bypass };
    save.mutate({ id: rule?.id, body }, { onSuccess: () => (notify.ok(t("saved")), close()), onError: notify.fail });
  }

  return (
    <form className="acc-form" onSubmit={submit}>
      <Input label={t("pattern")} required mono autoComplete="off" placeholder="v*" value={pattern} onChange={(e) => setPattern(e.target.value)} />
      <fieldset className="wk-fieldset">
        <legend>{t("tag.levels")}</legend>
        <p className="ui-note">{t("tag.levelsHint")}</p>
        {(["create", "update", "delete"] as const).map((a) => (
          <Select key={a} label={t(`tag.${a}`)} value={lv[a]} onValueChange={(x) => setLv((o) => ({ ...o, [a]: x as TagRule["create"] }))} options={options} />
        ))}
      </fieldset>
      <fieldset className="wk-fieldset">
        <legend>{t("tag.allowed")}</legend>
        <Input label={t("users")} hint={t("listHint")} mono value={users} onChange={(e) => setUsers(e.target.value)} />
        <Input label={t("teams")} hint={t("listHint")} mono value={teams} onChange={(e) => setTeams(e.target.value)} />
        <Check label={t("branch.f.adminsBypass")} hint={t("branch.f.adminsBypass.hint")} checked={bypass} onChange={setBypass} />
      </fieldset>
      <div className="acc-actions">
        <DialogClose asChild>
          <Button variant="ghost">{t("cancel")}</Button>
        </DialogClose>
        <Button variant="primary" type="submit" disabled={save.isPending || !pattern.trim()}>
          {t("save")}
        </Button>
      </div>
    </form>
  );
}
