import type { JobView, HttpCronJobSpec, Stats, RunView, ProjectView } from "./types";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: "include", ...init });
  if (res.status === 204) return undefined as T;
  let payload: unknown;
  try {
    payload = await res.json();
  } catch {
    payload = {};
  }
  if (!res.ok) {
    const msg =
      typeof payload === "object" && payload !== null && "error" in payload
        ? String((payload as { error: unknown }).error)
        : `HTTP ${res.status}`;
    throw new ApiError(res.status, msg);
  }
  return payload as T;
}

export const api = {
  login: (username: string, password: string) =>
    request<{ username: string }>("/api/v1/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    }),

  logout: () => request<void>("/api/v1/auth/logout", { method: "POST" }),

  me: () => request<{ username: string }>("/api/v1/me"),

  stats: () => request<Stats>("/api/v1/stats"),

  listJobs: (namespace?: string, project?: string) => {
    const params = new URLSearchParams();
    if (namespace) params.set("namespace", namespace);
    if (project) params.set("project", project);
    const qs = params.toString();
    return request<{ items: JobView[] }>("/api/v1/cronjobs" + (qs ? `?${qs}` : ""));
  },

  getJob: (namespace: string, name: string) =>
    request<JobView>(`/api/v1/cronjobs/${namespace}/${name}`),

  createJob: (name: string, namespace: string, spec: HttpCronJobSpec) =>
    request<JobView>("/api/v1/cronjobs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, namespace, spec }),
    }),

  createJobFromYaml: (manifest: string, dryRun = false) =>
    request<JobView>(`/api/v1/cronjobs${dryRun ? "?dryRun=true" : ""}`, {
      method: "POST",
      headers: { "Content-Type": "application/yaml" },
      body: manifest,
    }),

  updateJob: (namespace: string, name: string, spec: HttpCronJobSpec) =>
    request<JobView>(`/api/v1/cronjobs/${namespace}/${name}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ spec }),
    }),

  suspendJob: (namespace: string, name: string, suspend: boolean) =>
    request<JobView>(`/api/v1/cronjobs/${namespace}/${name}/suspend`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ suspend }),
    }),

  deleteJob: (namespace: string, name: string) =>
    request<void>(`/api/v1/cronjobs/${namespace}/${name}`, { method: "DELETE" }),

  runNow: (namespace: string, name: string) =>
    request<RunView>(`/api/v1/cronjobs/${namespace}/${name}/run`, { method: "POST" }),

  listRuns: (namespace: string, name: string) =>
    request<{ items: RunView[] }>(`/api/v1/cronjobs/${namespace}/${name}/runs`),

  listProjects: () => request<{ items: ProjectView[] }>("/api/v1/projects"),

  createProject: (name: string, description: string) =>
    request<ProjectView>("/api/v1/projects", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, description }),
    }),

  deleteProject: (name: string) =>
    request<void>(`/api/v1/projects/${encodeURIComponent(name)}`, { method: "DELETE" }),
};
