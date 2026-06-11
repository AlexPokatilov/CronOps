import { useMemo, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { stringify } from "yaml";
import { api } from "../api";
import type { AuthSpec, HeaderSpec, HttpCronJobSpec } from "../types";

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"] as const;
const AUTH_TYPES = ["none", "basic", "bearer", "apiKey"] as const;

const YAML_TEMPLATE = `apiVersion: cronops.io/v1alpha1
kind: HttpCronJob
metadata:
  name: my-job
  namespace: default
spec:
  schedule: "0 3 * * *"
  endpoint: https://api.example.com/v1/run
  method: POST
  # timezone: Europe/Kyiv
  # headers:
  #   - name: Content-Type
  #     value: application/json
  # auth:
  #   type: bearer
  #   secretRef:
  #     name: my-token-secret
  # body: '{"hello": "world"}'
  # timeoutSeconds: 30
  # concurrencyPolicy: Forbid
`;

interface FormState {
  name: string;
  namespace: string;
  schedule: string;
  endpoint: string;
  method: (typeof METHODS)[number];
  timezone: string;
  body: string;
  timeoutSeconds: string;
  concurrencyPolicy: "Forbid" | "Allow";
  historyLimit: string;
  authType: (typeof AUTH_TYPES)[number];
  authSecretName: string;
  authSecretKey: string;
  authHeaderName: string;
  headers: HeaderSpec[];
  captureResponseBody: boolean;
}

const initialForm: FormState = {
  name: "",
  namespace: "default",
  schedule: "",
  endpoint: "",
  method: "GET",
  timezone: "",
  body: "",
  timeoutSeconds: "",
  concurrencyPolicy: "Forbid",
  historyLimit: "",
  authType: "none",
  authSecretName: "",
  authSecretKey: "",
  authHeaderName: "",
  headers: [],
  captureResponseBody: true,
};

function buildSpec(f: FormState): HttpCronJobSpec {
  const spec: HttpCronJobSpec = {
    schedule: f.schedule,
    endpoint: f.endpoint,
    method: f.method,
  };
  if (f.timezone) spec.timezone = f.timezone;
  if (f.body) spec.body = f.body;
  if (f.timeoutSeconds) spec.timeoutSeconds = Number(f.timeoutSeconds);
  if (f.historyLimit) spec.historyLimit = Number(f.historyLimit);
  if (f.concurrencyPolicy !== "Forbid") spec.concurrencyPolicy = f.concurrencyPolicy;
  if (!f.captureResponseBody) spec.captureResponseBody = false;
  const headers = f.headers.filter((h) => h.name);
  if (headers.length > 0) spec.headers = headers;
  if (f.authType !== "none") {
    const auth: AuthSpec = { type: f.authType, secretRef: { name: f.authSecretName } };
    if (f.authSecretKey && auth.secretRef) auth.secretRef.key = f.authSecretKey;
    if (f.authType === "apiKey" && f.authHeaderName) auth.headerName = f.authHeaderName;
    spec.auth = auth;
  }
  return spec;
}

export function CronJobNewPage() {
  const [tab, setTab] = useState<"form" | "yaml">("form");
  const [form, setForm] = useState<FormState>(initialForm);
  const [manifest, setManifest] = useState(YAML_TEMPLATE);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  const yamlPreview = useMemo(() => {
    return stringify({
      apiVersion: "cronops.io/v1alpha1",
      kind: "HttpCronJob",
      metadata: { name: form.name || "my-job", namespace: form.namespace || "default" },
      spec: buildSpec(form),
    });
  }, [form]);

  const submitForm = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const job = await api.createJob(form.name, form.namespace || "default", buildSpec(form));
      navigate(`/cronjobs/${job.namespace}/${job.name}`);
    } catch (err) {
      setError(String((err as Error).message ?? err));
    } finally {
      setBusy(false);
    }
  };

  const submitYaml = async () => {
    setBusy(true);
    setError("");
    try {
      const job = await api.createJobFromYaml(manifest);
      navigate(`/cronjobs/${job.namespace}/${job.name}`);
    } catch (err) {
      setError(String((err as Error).message ?? err));
    } finally {
      setBusy(false);
    }
  };

  const validateYaml = async () => {
    setBusy(true);
    setError("");
    try {
      await api.createJobFromYaml(manifest, true);
      setError("");
      window.alert("Manifest is valid ✔");
    } catch (err) {
      setError(String((err as Error).message ?? err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <h1>New CronJob</h1>
      <div className="tabs">
        <button className={tab === "form" ? "active" : ""} onClick={() => setTab("form")}>
          Form
        </button>
        <button className={tab === "yaml" ? "active" : ""} onClick={() => setTab("yaml")}>
          YAML manifest
        </button>
      </div>
      {error && <div className="error-box">{error}</div>}

      {tab === "form" ? (
        <div className="row" style={{ alignItems: "flex-start" }}>
          <form className="panel" style={{ marginTop: 0 }} onSubmit={submitForm}>
            <div className="row">
              <div className="field">
                <label className="required">Name</label>
                <input
                  value={form.name}
                  onChange={(e) => set("name", e.target.value)}
                  pattern="[a-z0-9]([-a-z0-9]*[a-z0-9])?"
                  title="lowercase alphanumeric and dashes (RFC 1123)"
                  required
                />
              </div>
              <div className="field">
                <label>Namespace</label>
                <input
                  value={form.namespace}
                  onChange={(e) => set("namespace", e.target.value)}
                  placeholder="default"
                />
              </div>
            </div>
            <div className="row">
              <div className="field">
                <label className="required">Schedule (cron)</label>
                <input
                  value={form.schedule}
                  onChange={(e) => set("schedule", e.target.value)}
                  placeholder="0 3 * * *"
                  required
                />
              </div>
              <div className="field">
                <label>Timezone</label>
                <input
                  value={form.timezone}
                  onChange={(e) => set("timezone", e.target.value)}
                  placeholder="UTC"
                />
              </div>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 3 }}>
                <label className="required">Endpoint URL</label>
                <input
                  type="url"
                  value={form.endpoint}
                  onChange={(e) => set("endpoint", e.target.value)}
                  placeholder="https://api.example.com/v1/run"
                  required
                />
              </div>
              <div className="field">
                <label className="required">Method</label>
                <select value={form.method} onChange={(e) => set("method", e.target.value as FormState["method"])}>
                  {METHODS.map((m) => (
                    <option key={m}>{m}</option>
                  ))}
                </select>
              </div>
            </div>

            <fieldset>
              <legend>Headers</legend>
              {form.headers.map((h, i) => (
                <div className="row" key={i} style={{ marginBottom: 8 }}>
                  <input
                    placeholder="Name"
                    value={h.name}
                    onChange={(e) =>
                      set(
                        "headers",
                        form.headers.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)),
                      )
                    }
                  />
                  <input
                    placeholder="Value"
                    value={h.value}
                    onChange={(e) =>
                      set(
                        "headers",
                        form.headers.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)),
                      )
                    }
                  />
                  <button
                    type="button"
                    className="secondary small"
                    style={{ flex: "0 0 auto" }}
                    onClick={() => set("headers", form.headers.filter((_, j) => j !== i))}
                  >
                    ✕
                  </button>
                </div>
              ))}
              <button
                type="button"
                className="secondary small"
                onClick={() => set("headers", [...form.headers, { name: "", value: "" }])}
              >
                + Add header
              </button>
            </fieldset>

            <fieldset>
              <legend>Authentication</legend>
              <div className="row">
                <div className="field">
                  <label>Type</label>
                  <select
                    value={form.authType}
                    onChange={(e) => set("authType", e.target.value as FormState["authType"])}
                  >
                    {AUTH_TYPES.map((t) => (
                      <option key={t}>{t}</option>
                    ))}
                  </select>
                </div>
                {form.authType !== "none" && (
                  <div className="field">
                    <label className="required">Secret name</label>
                    <input
                      value={form.authSecretName}
                      onChange={(e) => set("authSecretName", e.target.value)}
                      required
                    />
                  </div>
                )}
                {(form.authType === "bearer" || form.authType === "apiKey") && (
                  <div className="field">
                    <label>Secret key</label>
                    <input
                      value={form.authSecretKey}
                      onChange={(e) => set("authSecretKey", e.target.value)}
                      placeholder="token"
                    />
                  </div>
                )}
                {form.authType === "apiKey" && (
                  <div className="field">
                    <label>Header name</label>
                    <input
                      value={form.authHeaderName}
                      onChange={(e) => set("authHeaderName", e.target.value)}
                      placeholder="X-API-Key"
                    />
                  </div>
                )}
              </div>
              {form.authType === "basic" && (
                <div className="info-box" style={{ marginBottom: 0 }}>
                  The Secret must contain <code>username</code> and <code>password</code> keys.
                </div>
              )}
            </fieldset>

            <div className="field">
              <label>Request body</label>
              <textarea
                value={form.body}
                onChange={(e) => set("body", e.target.value)}
                placeholder='{"period": "daily"}'
              />
            </div>
            <div className="row">
              <div className="field">
                <label>Timeout (seconds)</label>
                <input
                  type="number"
                  min={1}
                  max={3600}
                  value={form.timeoutSeconds}
                  onChange={(e) => set("timeoutSeconds", e.target.value)}
                  placeholder="30"
                />
              </div>
              <div className="field">
                <label>Concurrency policy</label>
                <select
                  value={form.concurrencyPolicy}
                  onChange={(e) => set("concurrencyPolicy", e.target.value as FormState["concurrencyPolicy"])}
                >
                  <option>Forbid</option>
                  <option>Allow</option>
                </select>
              </div>
              <div className="field">
                <label>History limit</label>
                <input
                  type="number"
                  min={1}
                  max={50}
                  value={form.historyLimit}
                  onChange={(e) => set("historyLimit", e.target.value)}
                  placeholder="10"
                />
              </div>
            </div>

            <div className="field">
              <label style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer" }}>
                <input
                  type="checkbox"
                  style={{ width: "auto" }}
                  checked={form.captureResponseBody}
                  onChange={(e) => set("captureResponseBody", e.target.checked)}
                />
                Capture response body in run history (first 2 KiB; disable for sensitive responses)
              </label>
            </div>

            <div className="actions">
              <button type="submit" disabled={busy}>
                {busy ? "Creating…" : "Create CronJob"}
              </button>
            </div>
          </form>

          <div className="panel" style={{ marginTop: 0, flex: "0 0 38%" }}>
            <h2 style={{ marginTop: 0 }}>Manifest preview</h2>
            <pre className="yaml-preview">{yamlPreview}</pre>
          </div>
        </div>
      ) : (
        <div className="panel" style={{ marginTop: 0 }}>
          <div className="field">
            <label>
              Paste a <code>HttpCronJob</code> manifest (apiVersion <code>cronops.io/v1alpha1</code>)
            </label>
            <textarea
              className="yaml"
              value={manifest}
              onChange={(e) => setManifest(e.target.value)}
              spellCheck={false}
            />
          </div>
          <div className="actions">
            <button onClick={submitYaml} disabled={busy}>
              {busy ? "Creating…" : "Create from YAML"}
            </button>
            <button className="secondary" onClick={validateYaml} disabled={busy}>
              Validate (dry-run)
            </button>
          </div>
        </div>
      )}
    </>
  );
}
