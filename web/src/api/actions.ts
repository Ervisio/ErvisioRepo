import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { qs, repoBase } from "./code.ts";
import { request } from "./http.ts";
import type { components } from "./schema";

// GitHub-style Actions: workflows, runs, jobs, logs, secrets, variables, environments, runners.
type S = components["schemas"];
export type ActionsWorkflow = S["ActionsWorkflow"];
export type ActionsRun = S["ActionsRun"];
export type ActionsRunList = S["ActionsRunList"];
export type ActionsJob = S["ActionsJob"];
export type ActionsStep = S["ActionsStep"];
export type ActionsJobDetail = S["ActionsJobDetail"];
export type ActionsLogLine = S["ActionsLogLine"];
export type ActionsLogPage = S["ActionsLogPage"];
export type ActionsSecret = S["ActionsSecret"];
export type ActionsVariable = S["ActionsVariable"];
export type ActionsEnvironment = S["ActionsEnvironment"];
export type ActionsEnvironmentPut = S["ActionsEnvironmentPut"];
export type ActionsRunner = S["ActionsRunner"];
export type ActionsRegistrationToken = S["ActionsRegistrationToken"];

const enc = encodeURIComponent;
export const RUNS_PER_PAGE = 30;

/** Collection base of the Actions endpoints of a repository, an organization or the instance. */
export type ActionsScope = { kind: "repo"; owner: string; name: string } | { kind: "org"; org: string } | { kind: "instance" };
export const scopeBase = (s: ActionsScope) =>
  s.kind === "repo" ? `${repoBase(s.owner, s.name)}/actions` : s.kind === "org" ? `/orgs/${enc(s.org)}/actions` : "/admin/actions";
const scopeKey = (s: ActionsScope) => (s.kind === "repo" ? `${s.owner}/${s.name}` : s.kind === "org" ? `org:${s.org}` : "instance");

export type RunFilter = { workflow?: string; event?: string; status?: string; branch?: string; page?: number };

export const useWorkflows = (owner: string, name: string) =>
  useQuery({ queryKey: ["actions", owner, name, "workflows"], queryFn: () => request<ActionsWorkflow[]>("GET", `${repoBase(owner, name)}/actions/workflows`) });

export const useRuns = (owner: string, name: string, f: RunFilter) =>
  useQuery({
    queryKey: ["actions", owner, name, "runs", f],
    queryFn: () =>
      request<ActionsRunList>(
        "GET",
        `${repoBase(owner, name)}/actions/runs${qs({ workflow: f.workflow, event: f.event, status: f.status, branch: f.branch, page: f.page, perPage: RUNS_PER_PAGE })}`,
      ),
    placeholderData: keepPreviousData,
    refetchInterval: 10_000,
  });

const live = (run?: ActionsRun) => run !== undefined && run.status !== "completed";

export const useRun = (owner: string, name: string, runId: string) =>
  useQuery({
    queryKey: ["actions", owner, name, "run", runId],
    queryFn: () => request<ActionsRun>("GET", `${repoBase(owner, name)}/actions/runs/${enc(runId)}`),
    refetchInterval: (q) => (live(q.state.data) ? 4000 : false),
  });

export const useRunJobs = (owner: string, name: string, runId: string, run?: ActionsRun) =>
  useQuery({
    queryKey: ["actions", owner, name, "run", runId, "jobs"],
    queryFn: () => request<ActionsJob[]>("GET", `${repoBase(owner, name)}/actions/runs/${enc(runId)}/jobs`),
    refetchInterval: live(run) ? 4000 : false,
  });

export const useJob = (owner: string, name: string, jobId: string, running: boolean) =>
  useQuery({
    queryKey: ["actions", owner, name, "job", jobId],
    queryFn: () => request<ActionsJobDetail>("GET", `${repoBase(owner, name)}/actions/jobs/${enc(jobId)}`),
    refetchInterval: running ? 4000 : false,
  });

export const jobLogPath = (owner: string, name: string, jobId: string) => `${repoBase(owner, name)}/actions/jobs/${enc(jobId)}/logs`;
export const fetchLogPage = (owner: string, name: string, jobId: string, from: number) =>
  request<ActionsLogPage>("GET", `${jobLogPath(owner, name, jobId)}${qs({ from, limit: 1000 })}`);
export const logRawUrl = (owner: string, name: string, jobId: string) => `/api/v1${jobLogPath(owner, name, jobId)}/raw`;
export const logStreamUrl = (owner: string, name: string, jobId: string, from: number) => `/api/v1${jobLogPath(owner, name, jobId)}/stream${qs({ from })}`;

function useActionsMutation<V, R>(owner: string, name: string, fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: ["actions", owner, name] }) });
}

const runPath = (owner: string, name: string, runId: string) => `${repoBase(owner, name)}/actions/runs/${enc(runId)}`;
export const useRunAction = (owner: string, name: string, runId: string, action: "rerun" | "cancel" | "approve") =>
  useActionsMutation<void, void>(owner, name, () => request("POST", `${runPath(owner, name, runId)}/${action}`));

export const useCancelJob = (owner: string, name: string) =>
  useActionsMutation<string, void>(owner, name, (jobId) => request("POST", `${repoBase(owner, name)}/actions/jobs/${enc(jobId)}/cancel`));
export const useReviewDeployment = (owner: string, name: string) =>
  useActionsMutation<{ jobId: string; approve: boolean }, void>(owner, name, ({ jobId, approve }) =>
    request("POST", `${repoBase(owner, name)}/actions/jobs/${enc(jobId)}/deployment`, { approve }),
  );

export const useDispatch = (owner: string, name: string) =>
  useActionsMutation<{ workflow: string; ref?: string; inputs?: Record<string, string> }, void>(owner, name, ({ workflow, ...body }) =>
    request("POST", `${repoBase(owner, name)}/actions/workflows/${enc(workflow)}/dispatches`, body),
  );

/* Secrets and variables: repository, organization (and, for the repository, per environment). */

type Kind = "secrets" | "variables";
export const useEntries = <T>(s: ActionsScope, kind: Kind) =>
  useQuery({ queryKey: ["actions-cfg", scopeKey(s), kind], queryFn: () => request<T[]>("GET", `${scopeBase(s)}/${kind}`) });

export function usePutEntry(s: ActionsScope, kind: Kind) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, value }: { name: string; value: string }) => request("PUT", `${scopeBase(s)}/${kind}/${enc(name)}`, { value }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["actions-cfg", scopeKey(s), kind] }),
  });
}
export function useDeleteEntry(s: ActionsScope, kind: Kind) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => request("DELETE", `${scopeBase(s)}/${kind}/${enc(name)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["actions-cfg", scopeKey(s), kind] }),
  });
}

/* Environments (repository only). */

export const useEnvironments = (owner: string, name: string) =>
  useQuery({ queryKey: ["actions-cfg", `${owner}/${name}`, "environments"], queryFn: () => request<ActionsEnvironment[]>("GET", `${repoBase(owner, name)}/actions/environments`) });
export function usePutEnvironment(owner: string, name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ env, body }: { env: string; body: ActionsEnvironmentPut }) => request("PUT", `${repoBase(owner, name)}/actions/environments/${enc(env)}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["actions-cfg", `${owner}/${name}`, "environments"] }),
  });
}
export function useDeleteEnvironment(owner: string, name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (env: string) => request("DELETE", `${repoBase(owner, name)}/actions/environments/${enc(env)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["actions-cfg", `${owner}/${name}`, "environments"] }),
  });
}

/* Runners and registration tokens. */

export const useRunners = (s: ActionsScope) =>
  useQuery({ queryKey: ["actions-cfg", scopeKey(s), "runners"], queryFn: () => request<ActionsRunner[]>("GET", `${scopeBase(s)}/runners`), refetchInterval: 15_000 });
export function useDeleteRunner(s: ActionsScope) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => request("DELETE", `${scopeBase(s)}/runners/${enc(id)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["actions-cfg", scopeKey(s), "runners"] }),
  });
}
export const useRegistrationToken = (s: ActionsScope) =>
  useMutation({ mutationFn: () => request<ActionsRegistrationToken>("POST", `${scopeBase(s)}/runners/registration-token`) });
