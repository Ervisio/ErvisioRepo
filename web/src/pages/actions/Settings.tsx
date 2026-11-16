import { Link } from "@tanstack/react-router";
import { useRef, useState, type FormEvent, type SyntheticEvent } from "react";
import {
  useGitHubActionsImport,
  useImportGitHubActions,
  useDeleteEntry,
  useDeleteEnvironment,
  useDeleteRunner,
  useEnvironments,
  usePutEntry,
  usePutEnvironment,
  useRegistrationToken,
  useRunners,
  useSetRunnerPaused,
  type ActionsRunner,
  type ActionsRegistrationToken,
  type ActionsScope,
  type ActionsSecret,
  type ActionsVariable,
} from "../../api/actions.ts";
import { useEntries } from "../../api/actions.ts";
import { useT } from "../../i18n/useT.ts";
import { useMeta } from "../../api/useMeta.ts";
import { Badge, type BadgeTone, Button, Card, Dialog, Input, Table, TableBody, TableHead, Tabs, TabsContent, TabsList, TabsTrigger, Td, Th, Tr } from "../../ui/index.ts";
import { Async, formatDate, Secret, useNotify } from "../account/common.tsx";

const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

/** Secrets (value is write-only) or variables of one scope. */
export function Entries({ scope, kind }: { scope: ActionsScope; kind: "secrets" | "variables" }) {
  const t = useT("actions");
  const notify = useNotify();
  const list = useEntries<ActionsSecret | ActionsVariable>(scope, kind);
  const put = usePutEntry(scope, kind);
  const del = useDeleteEntry(scope, kind);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const secret = kind === "secrets";
  const valid = NAME_RE.test(name) && (secret ? value !== "" : true);

  function submit(e: FormEvent) {
    e.preventDefault();
    put.mutate(
      { name, value },
      {
        onSuccess: () => {
          notify.ok(t(`${kind}.saved`));
          setName("");
          setValue("");
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <>
      <Card title={t(`${kind}.title`)}>
        <p className="ui-note acc-pad">{t(`${kind}.hint`)}</p>
        <Async query={list} empty={t(`${kind}.empty`)}>
          {(rows) => (
            <Table>
              <TableHead>
                <Tr>
                  <Th>{t("cfg.name")}</Th>
                  {!secret && <Th>{t("cfg.value")}</Th>}
                  <Th>{t("cfg.updated")}</Th>
                  <Th><span className="sr-only">{t("cfg.actions")}</span></Th>
                </Tr>
              </TableHead>
              <TableBody>
                {rows
                  .filter((r) => r.environment === "")
                  .map((r) => (
                    <Tr key={r.name}>
                      <Td><span className="ui-mono">{r.name}</span></Td>
                      {!secret && <Td><span className="ui-mono">{(r as ActionsVariable).value}</span></Td>}
                      <Td>{formatDate(r.updatedAt)}</Td>
                      <Td>
                        <Button size="sm" variant="ghost" disabled={del.isPending} aria-label={t("cfg.deleteOf", { name: r.name })} onClick={() => del.mutate(r.name, { onSuccess: () => notify.ok(t(`${kind}.deleted`)), onError: notify.fail })}>
                          {t("cfg.delete")}
                        </Button>
                      </Td>
                    </Tr>
                  ))}
              </TableBody>
            </Table>
          )}
        </Async>
      </Card>
      <Card title={t(`${kind}.add`)}>
        <form className="acc-form" onSubmit={submit}>
          <Input label={t("cfg.name")} mono required autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} hint={t("cfg.nameHint")} />
          {secret ? (
            <Input label={t("cfg.secretValue")} type="password" required autoComplete="new-password" value={value} onChange={(e) => setValue(e.target.value)} hint={t("cfg.secretHint")} />
          ) : (
            <Input label={t("cfg.value")} mono autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} />
          )}
          <div className="acc-actions">
            <Button variant="primary" type="submit" disabled={!valid || put.isPending}>{t("cfg.save")}</Button>
          </div>
        </form>
      </Card>
    </>
  );
}

/** Repository environments: approval requirement and reviewers (usernames). */
export function Environments({ owner, name }: { owner: string; name: string }) {
  const t = useT("actions");
  const notify = useNotify();
  const list = useEnvironments(owner, name);
  const put = usePutEnvironment(owner, name);
  const del = useDeleteEnvironment(owner, name);
  const [env, setEnv] = useState("");
  const [approval, setApproval] = useState(false);
  const [reviewers, setReviewers] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    const names = reviewers.split(/[\s,]+/).filter(Boolean);
    put.mutate(
      { env: env.trim(), body: { requireApproval: approval, reviewers: names } },
      {
        onSuccess: () => {
          notify.ok(t("environments.saved"));
          setEnv("");
          setReviewers("");
          setApproval(false);
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <>
      <Card title={t("environments.title")}>
        <p className="ui-note acc-pad">{t("environments.hint")}</p>
        <Async query={list} empty={t("environments.empty")}>
          {(rows) => (
            <Table>
              <TableHead>
                <Tr>
                  <Th>{t("cfg.name")}</Th>
                  <Th>{t("environments.approval")}</Th>
                  <Th>{t("environments.reviewers")}</Th>
                  <Th><span className="sr-only">{t("cfg.actions")}</span></Th>
                </Tr>
              </TableHead>
              <TableBody>
                {rows.map((r) => (
                  <Tr key={r.name}>
                    <Td><span className="ui-mono">{r.name}</span></Td>
                    <Td>{r.requireApproval ? <Badge tone="warn">{t("environments.required")}</Badge> : <span className="ui-note">{t("environments.none")}</span>}</Td>
                    <Td>{r.reviewers.length > 0 ? t("environments.count", { count: r.reviewers.length }) : t("environments.maintainers")}</Td>
                    <Td>
                      <Button size="sm" variant="ghost" disabled={del.isPending} aria-label={t("cfg.deleteOf", { name: r.name })} onClick={() => del.mutate(r.name, { onSuccess: () => notify.ok(t("environments.deleted")), onError: notify.fail })}>
                        {t("cfg.delete")}
                      </Button>
                    </Td>
                  </Tr>
                ))}
              </TableBody>
            </Table>
          )}
        </Async>
      </Card>
      <Card title={t("environments.add")}>
        <form className="acc-form" onSubmit={submit}>
          <Input label={t("cfg.name")} mono required autoComplete="off" value={env} onChange={(e) => setEnv(e.target.value)} />
          <label className="acc-toggle">
            <input type="checkbox" checked={approval} onChange={(e) => setApproval(e.target.checked)} />
            <span>{t("environments.requireApproval")}</span>
          </label>
          <Input label={t("environments.reviewersLabel")} mono autoComplete="off" value={reviewers} onChange={(e) => setReviewers(e.target.value)} hint={t("environments.reviewersHint")} />
          <div className="acc-actions">
            <Button variant="primary" type="submit" disabled={env.trim() === "" || put.isPending}>{t("cfg.save")}</Button>
          </div>
        </form>
      </Card>
    </>
  );
}

/** Name the built-in runner registers with (setup.sh, runner-setup). */
const BUILTIN_RUNNER = "builtin";

const STATUS_TONE: Record<ActionsRunner["status"], BadgeTone> = { offline: "neutral", paused: "warn", idle: "ok", online: "ok", busy: "info" };

function RunnerStatus({ runner }: { runner: ActionsRunner }) {
  const t = useT("actions");
  return <Badge tone={STATUS_TONE[runner.status]}>{t(`runners.st.${runner.status}`)}</Badge>;
}

/** Scope string the server reports for runners registered at this scope. */
const scopeId = (s: ActionsScope) => (s.kind === "instance" ? "instance" : s.kind === "org" ? `org:${s.org}` : `repo:${s.owner}/${s.name}`).toLowerCase();

/**
 * Runners of one scope. The instance pool is the default for every
 * repository; org and repository runners are an optional extra.
 */
export function Runners({ scope }: { scope: ActionsScope }) {
  const t = useT("actions");
  const notify = useNotify();
  const list = useRunners(scope);
  const del = useDeleteRunner(scope);
  const pause = useSetRunnerPaused(scope);
  const [adding, setAdding] = useState(false);
  const instance = scope.kind === "instance";
  const builtin = instance ? list.data?.find((r) => r.name === BUILTIN_RUNNER) : undefined;
  const mine = scopeId(scope);

  return (
    <>
      {instance && list.data && (
        <Card title={t("runners.builtin")}>
          {builtin ? (
            <div className="ui-row">
              <RunnerStatus runner={builtin} />
              <span className="ui-mono">{builtin.labels.join(", ")}</span>
              {builtin.version && <span className="ui-note">v{builtin.version}</span>}
            </div>
          ) : (
            <p className="ui-note acc-pad">{t("runners.builtinNone")}</p>
          )}
        </Card>
      )}
      <Card
        title={instance ? t("runners.title") : t("runners.selfHosted")}
        actions={
          <Button size="sm" variant={instance ? "primary" : "secondary"} icon="plus" onClick={() => setAdding(true)}>
            {t("runners.new")}
          </Button>
        }
      >
        <p className="ui-note acc-pad">{instance ? t("runners.hintInstance") : t("runners.hintScoped")}</p>
        <Async query={list} empty={t("runners.empty")}>
          {(rows) => (
            <Table>
              <TableHead>
                <Tr>
                  <Th>{t("cfg.name")}</Th>
                  <Th>{t("runners.status")}</Th>
                  <Th>{t("runners.jobs")}</Th>
                  <Th>{t("runners.labels")}</Th>
                  <Th>{t("runners.version")}</Th>
                  <Th>{t("runners.seen")}</Th>
                  <Th>{t("runners.scope")}</Th>
                  <Th><span className="sr-only">{t("cfg.actions")}</span></Th>
                </Tr>
              </TableHead>
              <TableBody>
                {rows.map((r) => (
                  <Tr key={r.id}>
                    <Td><b>{r.name}</b> {r.ephemeral && <Badge>{t("runners.ephemeral")}</Badge>}</Td>
                    <Td><RunnerStatus runner={r} /></Td>
                    <Td>{r.capacity > 0 ? `${r.running} / ${r.capacity}` : r.running}</Td>
                    <Td><span className="ui-mono">{r.labels.join(", ")}</span></Td>
                    <Td>{r.version ? <span className="ui-mono">{r.version}</span> : "–"}</Td>
                    <Td>{r.lastSeenAt ? formatDate(r.lastSeenAt) : "–"}</Td>
                    <Td><span className="ui-mono">{r.scope}</span></Td>
                    <Td>
                      {r.scope.toLowerCase() === mine && (
                        <div className="rn-actions">
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={pause.isPending}
                            aria-label={t(r.paused ? "runners.resumeOf" : "runners.pauseOf", { name: r.name })}
                            onClick={() =>
                              pause.mutate(
                                { id: r.id, paused: !r.paused },
                                { onSuccess: () => notify.ok(t(r.paused ? "runners.resumed" : "runners.paused")), onError: notify.fail },
                              )
                            }
                          >
                            {t(r.paused ? "runners.resume" : "runners.pause")}
                          </Button>
                          <Button size="sm" variant="ghost" disabled={del.isPending} aria-label={t("cfg.deleteOf", { name: r.name })} onClick={() => del.mutate(r.id, { onSuccess: () => notify.ok(t("runners.deleted")), onError: notify.fail })}>
                            {t("cfg.delete")}
                          </Button>
                        </div>
                      )}
                    </Td>
                  </Tr>
                ))}
              </TableBody>
            </Table>
          )}
        </Async>
      </Card>
      <Dialog open={adding} onOpenChange={setAdding} title={t("runners.new")} description={t("runners.newHint")}>
        {adding && <NewRunner scope={scope} />}
      </Dialog>
    </>
  );
}

type RunnerOS = "linux" | "macos" | "windows" | "docker";

const SAFE_NAME = /[^A-Za-z0-9._-]/g;
const SAFE_LABELS = /[^A-Za-z0-9._,-]/g;

/** Install instructions for a new runner: the device-approval one-liner first, token commands under "Advanced". */
function NewRunner({ scope }: { scope: ActionsScope }) {
  const t = useT("actions");
  const notify = useNotify();
  const meta = useMeta();
  const gen = useRegistrationToken(scope);
  const [token, setToken] = useState<ActionsRegistrationToken | null>(null);
  const [name, setName] = useState("");
  const [labels, setLabels] = useState("");
  const [capacity, setCapacity] = useState("");
  const [os, setOS] = useState<RunnerOS>("linux");
  const { mutate } = gen;

  const started = useRef(false);
  // The registration token is created the first time the advanced section opens.
  const onToggle = (e: SyntheticEvent<HTMLDetailsElement>) => {
    if (!e.currentTarget.open || started.current) return;
    started.current = true;
    mutate(undefined, { onSuccess: setToken, onError: notify.fail });
  };

  if (!meta.data) return <p className="ui-note">{t("runners.creating")}</p>;

  const url = meta.data.publicUrl.replace(/\/+$/, "");
  const ver = /^\d+\.\d+\.\d+$/.test(meta.data.version) ? meta.data.version : "latest";
  const n = name.replace(SAFE_NAME, "");
  const l = labels.replace(SAFE_LABELS, "").replace(/^,+|,+$/g, "");
  const c = /^\d{1,2}$/.test(capacity) ? capacity : "";
  const commands = token
    ? {
        linux: `curl -fsSL ${url}/runner/install.sh | sudo sh -s -- --url ${url} --token ${token.token}${n ? ` --name ${n}` : ""}${l ? ` --labels ${l}` : ""}${c ? ` --capacity ${c}` : ""}`,
        macos: `curl -fsSL ${url}/runner/install.sh | sh -s -- --url ${url} --token ${token.token}${n ? ` --name ${n}` : ""}${l ? ` --labels ${l}` : ""}${c ? ` --capacity ${c}` : ""}`,
        windows: `& ([scriptblock]::Create((irm ${url}/runner/install.ps1))) -Url ${url} -Token ${token.token}${n ? ` -Name ${n}` : ""}${l ? ` -Labels ${l}` : ""}${c ? ` -Capacity ${c}` : ""}`,
        docker: [
          "docker run -d --name ervisio-runner --restart unless-stopped --user root --no-healthcheck \\",
          "  -v /var/run/docker.sock:/var/run/docker.sock -v ervisio-runner:/var/lib/ervisio-runner \\",
          `  -e ERVISIO_RUNNER_URL=${url} -e ERVISIO_RUNNER_TOKEN=${token.token} \\`,
          "  -e ERVISIO_RUNNER_DATA_DIR=/var/lib/ervisio-runner -e HOME=/var/lib/ervisio-runner \\",
          ...(n || l || c ? [`  ${[n && `-e ERVISIO_RUNNER_NAME=${n}`, l && `-e ERVISIO_RUNNER_LABELS=${l}`, c && `-e ERVISIO_RUNNER_CAPACITY=${c}`].filter(Boolean).join(" ")} \\`] : []),
          `  --entrypoint ervisio-runner ghcr.io/ervisio/ervisio-repo:${ver} \\`,
          "  daemon --config /var/lib/ervisio-runner/config.toml",
        ].join("\n"),
      }
    : null;

  return (
    <div className="rn-new">
      <Secret label={t("runners.oneLiner")} value={`curl -fsSL ${url}/runner/install.sh | sudo sh`} />
      <p className="ui-note">
        {t("runners.oneLinerHint")} <a href={`${url}/runner/approve`}>{`${url}/runner/approve`}</a>
      </p>
      <p className="ui-note">{t("runners.otherOS")}</p>
      <Secret label={t("runners.os.macosNoSudo")} value={`curl -fsSL ${url}/runner/install.sh | sh`} />
      <Secret label={t("runners.os.windowsDevice")} value={`irm ${url}/runner/install.ps1 | iex`} />
      <details className="rn-adv" onToggle={onToggle}>
        <summary>{t("runners.advanced")}</summary>
        {!commands || !token ? (
          <p className="ui-note">{t("runners.creating")}</p>
        ) : (
          <div className="rn-new">
            <p className="ui-note">{t("runners.tokenLabel", { when: formatDate(token.expiresAt) })}</p>
            <div className="rn-fields">
              <Input label={t("runners.name")} placeholder={t("runners.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} mono />
              <Input label={t("runners.extraLabels")} hint={t("runners.extraLabelsHint")} placeholder="gpu,big" value={labels} onChange={(e) => setLabels(e.target.value)} mono />
              <Input label={t("runners.capacity")} hint={t("runners.capacityHint")} inputMode="numeric" value={capacity} onChange={(e) => setCapacity(e.target.value)} mono />
            </div>
            <Tabs value={os} onValueChange={(v) => setOS(v as RunnerOS)}>
              <TabsList aria-label={t("runners.platform")}>
                <TabsTrigger value="linux">Linux</TabsTrigger>
                <TabsTrigger value="macos">macOS</TabsTrigger>
                <TabsTrigger value="windows">Windows</TabsTrigger>
                <TabsTrigger value="docker">Docker</TabsTrigger>
              </TabsList>
              {(Object.keys(commands) as RunnerOS[]).map((k) => (
                <TabsContent key={k} value={k}>
                  <Secret label={t(`runners.os.${k}`)} value={commands[k]} multiline={k === "docker"} />
                </TabsContent>
              ))}
            </Tabs>
            <p className="ui-note">{t("runners.backendNote")}</p>
            <Secret label={t("runners.tokenOnly")} value={token.token} />
          </div>
        )}
      </details>
    </div>
  );
}

/** Import of variables, secret names and run history from the GitHub counterpart of a repository; lists the secrets still to fill in. */
export function GitHubActionsImport({ owner, name }: { owner: string; name: string }) {
  const t = useT("actions");
  const notify = useNotify();
  const state = useGitHubActionsImport(owner, name);
  const run = useImportGitHubActions(owner, name);
  const [overwrite, setOverwrite] = useState(false);
  const g = state.data;
  if (!g?.available) return null;

  return (
    <Card title={t("gh.title")}>
      <p className="ui-note acc-pad">
        {t("gh.hint", { remote: g.remote })}{" "}
        {g.remoteUrl && (
          <a href={g.remoteUrl} target="_blank" rel="noreferrer">
            {t("gh.open")}
          </a>
        )}
      </p>
      {g.importedAt && (
        <p className="ui-note acc-pad">
          {t("gh.last", { when: formatDate(g.importedAt), vars: g.variablesImported, skipped: g.variablesSkipped, runs: g.runsImported })}
        </p>
      )}
      {g.lastError && (
        <p className="ui-note acc-pad" role="alert">
          <Badge tone="err">{t("gh.error")}</Badge> {g.lastError}
        </p>
      )}
      {g.missingSecrets.length > 0 && (
        <div className="acc-pad">
          <p className="ui-note">{t("gh.missing", { count: g.missingSecrets.length })}</p>
          <ul>
            {g.missingSecrets.map((m) => (
              <li key={`${m.environment}/${m.name}`}>
                <span className="ui-mono">{m.name}</span> {m.environment && <Badge>{t("gh.env", { env: m.environment })}</Badge>}
              </li>
            ))}
          </ul>
          <Link to="/$owner/$name/settings" params={{ owner, name }} search={{ section: "secrets" }}>
            {t("gh.fill")}
          </Link>
        </div>
      )}
      <div className="acc-form acc-pad">
        <label className="acc-check">
          <input type="checkbox" checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)} />
          <span>{t("gh.overwrite")}</span>
        </label>
        <div className="acc-actions">
          <Button
            icon="download"
            disabled={run.isPending}
            onClick={() =>
              run.mutate(
                { overwriteVariables: overwrite, runs: true },
                { onSuccess: (r) => (r.lastError ? notify.fail(new Error(r.lastError)) : notify.ok(t("gh.done"))), onError: notify.fail },
              )
            }
          >
            {run.isPending ? t("gh.running") : t("gh.import")}
          </Button>
        </div>
      </div>
    </Card>
  );
}
