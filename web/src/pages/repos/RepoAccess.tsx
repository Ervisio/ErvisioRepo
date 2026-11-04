import { useId, useState, type FormEvent } from "react";
import { useUserSearch } from "../../api/identity.ts";
import {
  useCollaborators,
  useCreateLabel,
  useCreateMilestone,
  useDeleteLabel,
  useDeleteMilestone,
  useLabels,
  useMilestones,
  useRemoveCollaborator,
  useSetCollaborator,
  useUpdateLabel,
  useUpdateMilestone,
  type CollaboratorPermission,
  type Label,
  type Milestone,
} from "../../api/issues.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, ConfirmDialog, Dialog, DialogClose, Input, Select, SettingsCard, SettingsSection } from "../../ui/index.ts";
import { Async, formatDate, useNotify } from "../account/common.tsx";
import { LabelChip } from "../work/common.tsx";

// Repository settings sections for people and issue metadata: collaborators, labels and milestones.

const permissions: CollaboratorPermission[] = ["read", "triage", "write", "maintain", "admin"];

function usePermissionOptions() {
  const t = useT("repos");
  return permissions.map((p) => ({ value: p, label: t(`collab.perm.${p}`) }));
}

export function Collaborators({ owner, name }: { owner: string; name: string }) {
  const t = useT("repos");
  const notify = useNotify();
  const list = useCollaborators(owner, name);
  const set = useSetCollaborator(owner, name);
  const remove = useRemoveCollaborator(owner, name);
  const options = usePermissionOptions();
  const [user, setUser] = useState("");
  const [perm, setPerm] = useState<CollaboratorPermission>("write");
  const [removing, setRemoving] = useState<string>();
  const listId = useId();
  const search = useUserSearch(user, `${owner}/${name}`);

  function add(e: FormEvent) {
    e.preventDefault();
    const username = user.trim();
    set.mutate(
      { username, permission: perm },
      { onSuccess: () => { setUser(""); notify.ok(t("collab.added", { user: username })); }, onError: notify.fail },
    );
  }

  return (
    <SettingsSection title={t("settings.section.collaborators")} description={t("collab.hint")}>
      <SettingsCard>
        <form className="st-add" onSubmit={add}>
          <Input label={t("collab.username")} mono autoComplete="off" required list={listId} value={user} onChange={(e) => setUser(e.target.value)} />
          <datalist id={listId}>
            {(search.data ?? []).map((u) => <option key={u.username} value={u.username}>{u.displayName}</option>)}
          </datalist>
          <Select className="st-narrow" label={t("collab.permission")} value={perm} onValueChange={(v) => setPerm(v as CollaboratorPermission)} options={options} />
          <Button type="submit" disabled={set.isPending || user.trim() === ""}>{t("collab.add")}</Button>
        </form>
      </SettingsCard>
      <SettingsCard>
        <Async query={list} empty={t("collab.empty")}>
          {(rows) => (
            <div className="st-list">
              {rows.map((c) => (
                <div key={c.username} className="st-item">
                  <div className="st-item-m">
                    <b>{c.username}</b>
                    <p className="ui-note">{c.displayName ? `${c.displayName} · ` : ""}{t("collab.since", { date: formatDate(c.since) })}</p>
                  </div>
                  <div className="st-item-a">
                    <Select
                      hideLabel
                      label={t("collab.permissionFor", { user: c.username })}
                      value={c.permission}
                      onValueChange={(v) => set.mutate({ username: c.username, permission: v as CollaboratorPermission }, { onSuccess: () => notify.ok(t("collab.changed")), onError: notify.fail })}
                      options={options}
                    />
                    <Button variant="ghost" icon="trash" aria-label={t("collab.remove", { user: c.username })} onClick={() => setRemoving(c.username)} />
                  </div>
                </div>
              ))}
            </div>
          )}
        </Async>
      </SettingsCard>
      <ConfirmDialog
        open={removing !== undefined}
        onOpenChange={(o) => { if (!o) setRemoving(undefined); }}
        title={t("collab.removeTitle", { user: removing ?? "" })}
        description={t("collab.removeHint")}
        confirmLabel={t("collab.removeConfirm")}
        pending={remove.isPending}
        onConfirm={() => {
          if (!removing) return;
          remove.mutate(removing, { onSuccess: () => { setRemoving(undefined); notify.ok(t("collab.removed")); }, onError: notify.fail });
        }}
      />
    </SettingsSection>
  );
}

/* ---------- Labels ---------- */

const hex = (c: string) => `#${c.replace(/^#/, "")}`;
const unhex = (c: string) => c.replace(/^#/, "").toLowerCase();

function LabelFields({ value, onChange }: { value: { name: string; color: string; description: string }; onChange: (v: { name: string; color: string; description: string }) => void }) {
  const t = useT("repos");
  return (
    <>
      <Input label={t("labels.name")} required maxLength={50} value={value.name} onChange={(e) => onChange({ ...value, name: e.target.value })} />
      <Input className="st-narrow" label={t("labels.color")} type="color" value={hex(value.color)} onChange={(e) => onChange({ ...value, color: unhex(e.target.value) })} />
      <Input label={t("labels.description")} maxLength={100} value={value.description} onChange={(e) => onChange({ ...value, description: e.target.value })} />
    </>
  );
}

function EditLabel({ owner, name, label }: { owner: string; name: string; label: Label }) {
  const t = useT("repos");
  const notify = useNotify();
  const update = useUpdateLabel(owner, name);
  const [open, setOpen] = useState(false);
  const [v, setV] = useState({ name: label.name, color: label.color, description: label.description });
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => { setOpen(o); if (o) setV({ name: label.name, color: label.color, description: label.description }); }}
      title={t("labels.editTitle", { name: label.name })}
      trigger={<Button variant="ghost" icon="edit" aria-label={t("labels.edit", { name: label.name })} />}
      footer={
        <>
          <DialogClose asChild><Button variant="ghost">{t("cancel")}</Button></DialogClose>
          <Button
            variant="primary"
            disabled={update.isPending || v.name.trim() === ""}
            onClick={() => update.mutate({ label: label.name, body: { name: v.name.trim(), color: v.color, description: v.description.trim() } }, { onSuccess: () => { setOpen(false); notify.ok(t("labels.saved")); }, onError: notify.fail })}
          >
            {t("settings.save")}
          </Button>
        </>
      }
    >
      <div className="acc-form"><LabelFields value={v} onChange={setV} /></div>
    </Dialog>
  );
}

export function Labels({ owner, name }: { owner: string; name: string }) {
  const t = useT("repos");
  const notify = useNotify();
  const list = useLabels(owner, name);
  const create = useCreateLabel(owner, name);
  const remove = useDeleteLabel(owner, name);
  const blank = { name: "", color: "6e7bff", description: "" };
  const [draft, setDraft] = useState(blank);
  const [removing, setRemoving] = useState<string>();

  function add(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      { name: draft.name.trim(), color: draft.color, description: draft.description.trim() },
      { onSuccess: () => { setDraft(blank); notify.ok(t("labels.created")); }, onError: notify.fail },
    );
  }

  return (
    <SettingsSection title={t("settings.section.labels")} description={t("labels.hint")}>
      <SettingsCard>
        <form className="st-add" onSubmit={add}>
          <LabelFields value={draft} onChange={setDraft} />
          <Button type="submit" disabled={create.isPending || draft.name.trim() === ""}>{t("labels.add")}</Button>
        </form>
      </SettingsCard>
      <SettingsCard>
        <Async query={list} empty={t("labels.empty")}>
          {(rows) => (
            <div className="st-list">
              {rows.map((l) => (
                <div key={l.id} className="st-item">
                  <div className="st-item-m">
                    <LabelChip label={l} />
                    {l.description && <p className="ui-note">{l.description}</p>}
                  </div>
                  <div className="st-item-a">
                    <EditLabel owner={owner} name={name} label={l} />
                    <Button variant="ghost" icon="trash" aria-label={t("labels.delete", { name: l.name })} onClick={() => setRemoving(l.name)} />
                  </div>
                </div>
              ))}
            </div>
          )}
        </Async>
      </SettingsCard>
      <ConfirmDialog
        open={removing !== undefined}
        onOpenChange={(o) => { if (!o) setRemoving(undefined); }}
        title={t("labels.deleteTitle", { name: removing ?? "" })}
        description={t("labels.deleteHint")}
        confirmLabel={t("labels.deleteConfirm")}
        pending={remove.isPending}
        onConfirm={() => {
          if (!removing) return;
          remove.mutate(removing, { onSuccess: () => { setRemoving(undefined); notify.ok(t("labels.deleted")); }, onError: notify.fail });
        }}
      />
    </SettingsSection>
  );
}

/* ---------- Milestones ---------- */

/** A date input value (YYYY-MM-DD) as the RFC 3339 date-time the API takes; "" stays "". */
const toDue = (d: string) => (d ? `${d}T00:00:00Z` : "");
const fromDue = (iso?: string | null) => (iso ? iso.slice(0, 10) : "");

function EditMilestone({ owner, name, m }: { owner: string; name: string; m: Milestone }) {
  const t = useT("repos");
  const notify = useNotify();
  const update = useUpdateMilestone(owner, name);
  const [open, setOpen] = useState(false);
  const start = { title: m.title, description: m.description, due: fromDue(m.dueOn) };
  const [v, setV] = useState(start);
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => { setOpen(o); if (o) setV(start); }}
      title={t("milestones.editTitle", { name: m.title })}
      trigger={<Button variant="ghost" icon="edit" aria-label={t("milestones.edit", { name: m.title })} />}
      footer={
        <>
          <DialogClose asChild><Button variant="ghost">{t("cancel")}</Button></DialogClose>
          <Button
            variant="primary"
            disabled={update.isPending || v.title.trim() === ""}
            onClick={() => update.mutate({ id: m.id, body: { title: v.title.trim(), description: v.description.trim(), dueOn: toDue(v.due) } }, { onSuccess: () => { setOpen(false); notify.ok(t("milestones.saved")); }, onError: notify.fail })}
          >
            {t("settings.save")}
          </Button>
        </>
      }
    >
      <div className="acc-form">
        <Input label={t("milestones.titleLabel")} required maxLength={100} value={v.title} onChange={(e) => setV({ ...v, title: e.target.value })} />
        <Input label={t("milestones.description")} value={v.description} onChange={(e) => setV({ ...v, description: e.target.value })} />
        <Input label={t("milestones.due")} type="date" value={v.due} onChange={(e) => setV({ ...v, due: e.target.value })} />
      </div>
    </Dialog>
  );
}

export function Milestones({ owner, name }: { owner: string; name: string }) {
  const t = useT("repos");
  const notify = useNotify();
  const list = useMilestones(owner, name, "all");
  const create = useCreateMilestone(owner, name);
  const update = useUpdateMilestone(owner, name);
  const remove = useDeleteMilestone(owner, name);
  const [title, setTitle] = useState("");
  const [due, setDue] = useState("");
  const [removing, setRemoving] = useState<Milestone>();

  function add(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      { title: title.trim(), ...(due ? { dueOn: toDue(due) } : {}) },
      { onSuccess: () => { setTitle(""); setDue(""); notify.ok(t("milestones.created")); }, onError: notify.fail },
    );
  }

  return (
    <SettingsSection title={t("settings.section.milestones")} description={t("milestones.hint")}>
      <SettingsCard>
        <form className="st-add" onSubmit={add}>
          <Input label={t("milestones.titleLabel")} required maxLength={100} value={title} onChange={(e) => setTitle(e.target.value)} />
          <Input className="st-narrow" label={t("milestones.due")} type="date" value={due} onChange={(e) => setDue(e.target.value)} />
          <Button type="submit" disabled={create.isPending || title.trim() === ""}>{t("milestones.add")}</Button>
        </form>
      </SettingsCard>
      <SettingsCard>
        <Async query={list} empty={t("milestones.empty")}>
          {(rows) => (
            <div className="st-list">
              {rows.map((m) => (
                <div key={m.id} className="st-item">
                  <div className="st-item-m">
                    <b>{m.title}</b>{" "}
                    <Badge tone={m.state === "open" ? "ok" : "neutral"}>{t(`milestones.state.${m.state}`)}</Badge>
                    <p className="ui-note">
                      {t("milestones.counts", { open: m.openIssues, closed: m.closedIssues })}
                      {m.dueOn ? ` · ${t("milestones.dueOn", { date: new Date(m.dueOn).toLocaleDateString() })}` : ""}
                    </p>
                  </div>
                  <div className="st-item-a">
                    <Button
                      variant="ghost"
                      disabled={update.isPending}
                      onClick={() => update.mutate({ id: m.id, body: { state: m.state === "open" ? "closed" : "open" } }, { onError: notify.fail })}
                    >
                      {m.state === "open" ? t("milestones.close") : t("milestones.reopen")}
                    </Button>
                    <EditMilestone owner={owner} name={name} m={m} />
                    <Button variant="ghost" icon="trash" aria-label={t("milestones.delete", { name: m.title })} onClick={() => setRemoving(m)} />
                  </div>
                </div>
              ))}
            </div>
          )}
        </Async>
      </SettingsCard>
      <ConfirmDialog
        open={removing !== undefined}
        onOpenChange={(o) => { if (!o) setRemoving(undefined); }}
        title={t("milestones.deleteTitle", { name: removing?.title ?? "" })}
        description={t("milestones.deleteHint")}
        confirmLabel={t("milestones.deleteConfirm")}
        pending={remove.isPending}
        onConfirm={() => {
          if (!removing) return;
          remove.mutate(removing.id, { onSuccess: () => { setRemoving(undefined); notify.ok(t("milestones.deleted")); }, onError: notify.fail });
        }}
      />
    </SettingsSection>
  );
}
