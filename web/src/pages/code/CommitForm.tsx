import { useId } from "react";
import { useT } from "../../i18n/useT.ts";
import { Input } from "../../ui/index.ts";

export type CommitTarget = { message: string; mode: "direct" | "branch"; newBranch: string };

export const emptyTarget = (): CommitTarget => ({ message: "", mode: "direct", newBranch: "" });

/** Commit message plus "commit to <branch>" or "create a new branch". */
export function CommitFields({ value, onChange, branch, placeholder }: {
  value: CommitTarget;
  onChange: (v: CommitTarget) => void;
  branch: string;
  placeholder: string;
}) {
  const t = useT("code");
  const group = useId();
  return (
    <fieldset className="acc-form rp-commit-form">
      <legend className="sr-only">{t("edit.commitLegend")}</legend>
      <Input label={t("edit.message")} value={value.message} placeholder={placeholder} onChange={(e) => onChange({ ...value, message: e.target.value })} />
      <label className="acc-check">
        <input type="radio" name={group} checked={value.mode === "direct"} onChange={() => onChange({ ...value, mode: "direct" })} />
        <span>{t("edit.direct", { branch })}</span>
      </label>
      <label className="acc-check">
        <input type="radio" name={group} checked={value.mode === "branch"} onChange={() => onChange({ ...value, mode: "branch" })} />
        <span>{t("edit.newBranch")}</span>
      </label>
      {value.mode === "branch" && (
        <Input
          label={t("edit.branchName")}
          mono
          required
          value={value.newBranch}
          onChange={(e) => onChange({ ...value, newBranch: e.target.value })}
        />
      )}
    </fieldset>
  );
}

/** Fields of the contents API that depend on the commit target. */
export function targetBody(v: CommitTarget, branch: string, fallbackMessage: string) {
  return {
    branch,
    newBranch: v.mode === "branch" ? v.newBranch.trim() : undefined,
    message: v.message.trim() || fallbackMessage,
  };
}
