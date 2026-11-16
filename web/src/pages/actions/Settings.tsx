import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import {
  useGitHubActionsImport,
  useImportGitHubActions,
  useDeleteEntry,
  useDeleteEnvironment,
  useEnvironments,
  usePutEntry,
  usePutEnvironment,
  type ActionsScope,
  type ActionsSecret,
  type ActionsVariable,
} from "../../api/actions.ts";
import { useEntries } from "../../api/actions.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Input, Table, TableBody, TableHead, Td, Th, Tr } from "../../ui/index.ts";
import { Async, formatDate, useNotify } from "../account/common.tsx";

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
