export interface HeaderSpec {
  name: string;
  value: string;
}

export interface SecretKeyRef {
  name: string;
  key?: string;
}

export interface AuthSpec {
  type: "none" | "basic" | "bearer" | "apiKey";
  secretRef?: SecretKeyRef;
  headerName?: string;
}

export interface HttpCronJobSpec {
  schedule: string;
  endpoint: string;
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE" | "HEAD";
  timezone?: string;
  suspend?: boolean;
  headers?: HeaderSpec[];
  auth?: AuthSpec;
  body?: string;
  timeoutSeconds?: number;
  successHttpCodes?: string[];
  concurrencyPolicy?: "Allow" | "Forbid";
  historyLimit?: number;
}

export interface RunResult {
  startedAt: string;
  finishedAt?: string;
  result: "Success" | "Failed";
  httpStatusCode?: number;
  message?: string;
}

export interface Condition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
}

export interface HttpCronJobStatus {
  phase?: "Active" | "Suspended" | "Invalid";
  observedGeneration?: number;
  lastScheduleTime?: string;
  nextScheduleTime?: string;
  lastRun?: RunResult;
  history?: RunResult[];
  conditions?: Condition[];
}

export interface JobView {
  name: string;
  namespace: string;
  spec: HttpCronJobSpec;
  status: HttpCronJobStatus;
  createdAt: string;
}

export interface RecentRun extends RunResult {
  job: string;
  namespace: string;
}

export interface Stats {
  total: number;
  active: number;
  suspended: number;
  invalid: number;
  lastRuns: { success: number; failed: number };
  recentRuns: RecentRun[];
}
