import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useDecideRunnerDevice, useRunnerDevice, type ActionsRunnerDevice } from "../../api/actions.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Input } from "../../ui/index.ts";
import { formatDate } from "../account/common.tsx";
import { AuthFrame } from "./AuthFrame.tsx";
import { CodeBoxes, Details } from "./parts.tsx";
import { useProblemText } from "./useProblemText.ts";

type Done = "approved" | "denied";
type Target = "instance" | "org" | "repo";

const GIB = 1024 ** 3;
const splitLabels = (v: string) => v.split(",").map((x) => x.trim()).filter(Boolean);

/** Runner enrolment approval (/runner/approve): enter the code shown by install.sh, review the machine and pick its scope. */
export function RunnerApprove({ userCode }: { userCode?: string }) {
  const t = useT("actions");
  const problem = useProblemText();
  const [code, setCode] = useState(userCode ?? "");
  const [active, setActive] = useState(userCode?.trim() || undefined);
  const [done, setDone] = useState<Done>();
  const device = useRunnerDevice(active);

  const home = <Link to="/">{t("approve.home")}</Link>;
  if (done) {
    return (
      <AuthFrame title={t("approve.title")} footer={home}>
        <div role="status">
          <EmptyState
            icon={done === "approved" ? "check" : "x"}
            title={t(done === "approved" ? "approve.approved" : "approve.denied")}
            description={t(done === "approved" ? "approve.approvedBody" : "approve.deniedBody")}
          />
        </div>
      </AuthFrame>
    );
  }
  if (device.data && active) {
    return (
      <AuthFrame wide title={t("approve.reviewTitle")} subtitle={t("approve.reviewSub")} footer={home}>
        <ApproveForm device={device.data} onDone={setDone} />
      </AuthFrame>
    );
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    setActive(code.trim());
  }
  return (
    <AuthFrame title={t("approve.title")} subtitle={t("approve.subtitle")} footer={home}>
      <form className="auth-form" onSubmit={submit}>
        <Input
          label={t("approve.code")}
          mono
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          placeholder="XXXX-XXXX"
          maxLength={9}
          required
          value={code}
          onChange={(e) => setCode(e.target.value.toUpperCase())}
        />
        {active && device.isError && <p className="auth-error" role="alert">{problem(device.error)}</p>}
        <Button variant="primary" type="submit" disabled={device.isFetching || code.trim() === ""}>{t("approve.continue")}</Button>
      </form>
    </AuthFrame>
  );
}

function ApproveForm({ device, onDone }: { device: ActionsRunnerDevice; onDone: (d: Done) => void }) {
  const t = useT("actions");
  const problem = useProblemText();
  const decide = useDecideRunnerDevice(device.userCode);
  const [name, setName] = useState(device.name);
  const [labels, setLabels] = useState(device.labels.join(", "));
  const [capacity, setCapacity] = useState(String(device.capacity > 0 ? device.capacity : 1));
  const [target, setTarget] = useState<Target>(device.canInstance ? "instance" : "org");
  const [org, setOrg] = useState("");
  const [repo, setRepo] = useState("");
  const [error, setError] = useState<string>();

  const cap = Number(capacity);
  const capOk = Number.isInteger(cap) && cap >= 1 && cap <= 64;
  const scopeOk = target === "instance" || (target === "org" ? org.trim() !== "" : /^[^/\s]+\/[^/\s]+$/.test(repo.trim()));
  const scope = target === "instance" ? "instance" : target === "org" ? `org:${org.trim()}` : `repo:${repo.trim()}`;

  function send(approve: boolean) {
    setError(undefined);
    const body = approve ? { approve, scope, name: name.trim() || undefined, labels: splitLabels(labels), capacity: cap } : { approve };
    decide.mutate(body, { onSuccess: () => onDone(approve ? "approved" : "denied"), onError: (e) => setError(problem(e)) });
  }

  const targets: { id: Target; label: string }[] = [
    ...(device.canInstance ? [{ id: "instance" as const, label: t("approve.scopeInstance") }] : []),
    { id: "org", label: t("approve.scopeOrg") },
    { id: "repo", label: t("approve.scopeRepo") },
  ];

  return (
    <form
      className="auth-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (capOk && scopeOk) send(true);
      }}
    >
      <CodeBoxes code={device.userCode} label={t("approve.code")} />
      <p className="auth-note">{t("approve.compare")}</p>
      <Details
        rows={[
          { label: t("approve.host"), value: device.hostname, mono: true },
          { label: t("approve.system"), value: `${device.os} / ${device.arch}` },
          { label: t("approve.cpus"), value: device.cpus },
          { label: t("approve.memory"), value: `${(device.memoryBytes / GIB).toFixed(1)} GiB` },
          { label: "Docker", value: device.docker ? t("approve.yes") : t("approve.no") },
          { label: t("approve.version"), value: device.version, mono: true },
          { label: t("approve.ip"), value: device.requestIp || "—", mono: true },
          { label: t("approve.expires"), value: formatDate(device.expiresAt) },
        ]}
      />
      <Input label={t("runners.name")} value={name} onChange={(e) => setName(e.target.value)} mono />
      <Input label={t("runners.extraLabels")} hint={t("approve.labelsHint")} value={labels} onChange={(e) => setLabels(e.target.value)} mono />
      <Input label={t("runners.capacity")} hint={t("approve.capacityHint")} type="number" min={1} max={64} value={capacity} onChange={(e) => setCapacity(e.target.value)} error={capOk ? undefined : t("approve.capacityError")} mono />
      <fieldset className="auth-fs">
        <legend>{t("runners.scope")}</legend>
        <div className="ui-seg auth-seg">
          {targets.map((o) => (
            <button key={o.id} type="button" className="ui-segi" aria-pressed={target === o.id} onClick={() => setTarget(o.id)}>
              {o.label}
            </button>
          ))}
        </div>
      </fieldset>
      {target === "org" && <Input label={t("approve.orgName")} autoComplete="off" value={org} onChange={(e) => setOrg(e.target.value)} mono />}
      {target === "repo" && <Input label={t("approve.repoName")} placeholder="owner/name" autoComplete="off" value={repo} onChange={(e) => setRepo(e.target.value)} mono />}
      <p className="auth-note">{t("approve.warning")}</p>
      {error && <p className="auth-error" role="alert">{error}</p>}
      <div className="auth-actions">
        <Button type="button" disabled={decide.isPending} onClick={() => send(false)}>{t("approve.deny")}</Button>
        <Button variant="primary" type="submit" disabled={decide.isPending || !capOk || !scopeOk}>{t("approve.approve")}</Button>
      </div>
    </form>
  );
}
