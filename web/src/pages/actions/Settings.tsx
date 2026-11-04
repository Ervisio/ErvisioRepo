import { useState, type FormEvent } from "react";
import {
  useDeleteEntry,
  useDeleteEnvironment,
  useDeleteRunner,
  useEnvironments,
  usePutEntry,
  usePutEnvironment,
  useRegistrationToken,
  useRunners,
  type ActionsRegistrationToken,
  type ActionsScope,
  type ActionsSecret,
  type ActionsVariable,
} from "../../api/actions.ts";
import { useEntries } from "../../api/actions.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Input, Table, TableBody, TableHead, Td, Th, Tr } from "../../ui/index.ts";
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

/** Runners of one scope, with a registration token generated on demand and shown once. */
export function Runners({ scope }: { scope: ActionsScope }) {
  const t = useT("actions");
  const notify = useNotify();
  const list = useRunners(scope);
  const del = useDeleteRunner(scope);
  const gen = useRegistrationToken(scope);
  const [token, setToken] = useState<ActionsRegistrationToken | null>(null);

  return (
    <>
      <Card title={t("runners.title")}>
        <p className="ui-note acc-pad">{t("runners.hint")}</p>
        <Async query={list} empty={t("runners.empty")}>
          {(rows) => (
            <Table>
              <TableHead>
                <Tr>
                  <Th>{t("cfg.name")}</Th>
                  <Th>{t("runners.status")}</Th>
                  <Th>{t("runners.labels")}</Th>
                  <Th>{t("runners.scope")}</Th>
                  <Th>{t("runners.seen")}</Th>
                  <Th><span className="sr-only">{t("cfg.actions")}</span></Th>
                </Tr>
              </TableHead>
              <TableBody>
                {rows.map((r) => (
                  <Tr key={r.id}>
                    <Td><b>{r.name}</b> {r.ephemeral && <Badge>{t("runners.ephemeral")}</Badge>}</Td>
                    <Td>
                      <Badge tone={r.online ? "ok" : "neutral"}>{r.online ? (r.busy ? t("runners.busy") : t("runners.online")) : t("runners.offline")}</Badge>
                    </Td>
                    <Td><span className="ui-mono">{r.labels.join(", ")}</span></Td>
                    <Td><span className="ui-mono">{r.scope}</span></Td>
                    <Td>{r.lastSeenAt ? formatDate(r.lastSeenAt) : "–"}</Td>
                    <Td>
                      <Button size="sm" variant="ghost" disabled={del.isPending} aria-label={t("cfg.deleteOf", { name: r.name })} onClick={() => del.mutate(r.id, { onSuccess: () => notify.ok(t("runners.deleted")), onError: notify.fail })}>
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
      <Card title={t("runners.register")}>
        <p className="ui-note acc-pad">{t("runners.registerHint")}</p>
        {token && (
          <>
            <Secret label={t("runners.tokenLabel", { when: formatDate(token.expiresAt) })} value={token.token} />
            <p className="ui-note acc-pad">{t("runners.tokenOnce")}</p>
          </>
        )}
        <div className="acc-actions">
          <Button disabled={gen.isPending} onClick={() => gen.mutate(undefined, { onSuccess: setToken, onError: notify.fail })}>{t("runners.generate")}</Button>
        </div>
      </Card>
    </>
  );
}
