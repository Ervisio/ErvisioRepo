import { useId } from "react";
import { useT } from "../../i18n/useT.ts";
import { Input } from "../../ui/index.ts";

export type CommitTarget = { message: string; description: string; mode: "direct" | "branch"; newBranch: string };

export const emptyTarget = (): CommitTarget => ({ message: "", description: "", mode: "direct", newBranch: "" });

/**
 * Commit message, optional description, then "commit to <branch>" or "new branch and
 * pull request" as two option rows (round 018 A save dialog).
 */
export function CommitFields({ value, onChange, branch, placeholder, pull }: {
  value: CommitTarget;
  onChange: (v: CommitTarget) => void;
  branch: string;
  placeholder: string;
  /** The new-branch option also opens a pull request (the editor). */
  pull?: boolean;
}) {
  const t = useT("code");
  const group = useId();
  const descId = useId();
  return (
    <fieldset className="acc-form rp-commit-form">
      <legend className="sr-only">{t("edit.commitLegend")}</legend>
      <Input label={t("edit.message")} value={value.message} placeholder={placeholder} onChange={(e) => onChange({ ...value, message: e.target.value })} />
      <div className="ui-field">
        <label htmlFor={descId}>
          {t("edit.description")} <span className="ui-note">{t("edit.optional")}</span>
        </label>
        <textarea
          id={descId}
          className="ui-input acc-area"
          rows={3}
          value={value.description}
          placeholder={t("edit.descriptionHint")}
          onChange={(e) => onChange({ ...value, description: e.target.value })}
        />
      </div>
      <label className="rp-opt">
        <input type="radio" name={group} checked={value.mode === "direct"} onChange={() => onChange({ ...value, mode: "direct" })} />
        <span>{t("edit.direct", { branch })}</span>
      </label>
      <label className="rp-opt">
        <input type="radio" name={group} checked={value.mode === "branch"} onChange={() => onChange({ ...value, mode: "branch" })} />
        <span>
          {pull ? t("edit.newBranchPull") : t("edit.newBranch")}
          {value.mode !== "branch" && value.newBranch && <small className="ui-mono">{value.newBranch}</small>}
        </span>
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
  const subject = v.message.trim() || fallbackMessage;
  const body = v.description.trim();
  return {
    branch,
    newBranch: v.mode === "branch" ? v.newBranch.trim() : undefined,
    message: body ? `${subject}\n\n${body}` : subject,
  };
}
