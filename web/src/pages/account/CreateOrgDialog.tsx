import { useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useCreateOrg } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, Input } from "../../ui/index.ts";
import { useNotify } from "./common.tsx";

/** New-organization dialog, opened from the shell's New menu. */
export function CreateOrgDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT("account");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateOrg();
  const [name, setName] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      { name: name.trim() },
      {
        onSuccess: (o) => {
          onOpenChange(false);
          setName("");
          notify.ok(t("orgs.created"));
          void navigate({ to: "/orgs/$org", params: { org: o.name } });
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={t("orgs.dialogTitle")}>
      <form className="acc-form" onSubmit={submit}>
        <Input label={t("orgs.new")} mono required maxLength={39} autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={create.isPending}>{t("orgs.create")}</Button>
        </div>
      </form>
    </Dialog>
  );
}
