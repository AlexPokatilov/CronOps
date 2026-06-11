import { Fragment, useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { stringify } from "yaml";
import { api } from "../api";
import type { JobView, RunView } from "../types";
import { PhaseBadge, ResultBadge, formatTime } from "../components/badges";

export function CronJobDetailPage() {
  const { namespace = "", name = "" } = useParams();
  const [job, setJob] = useState<JobView | null>(null);
  const [runs, setRuns] = useState<RunView[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [expandedRun, setExpandedRun] = useState<string | null>(null);
  const navigate = useNavigate();

  const load = useCallback(() => {
    api
      .getJob(namespace, name)
      .then(setJob)
      .catch((e) => setError(String(e.message ?? e)));
    api
      .listRuns(namespace, name)
      .then((r) => setRuns(r.items))
      .catch(() => {});
  }, [namespace, name]);

  useEffect(() => {
    load();
    const t = setInterval(load, 10_000);
    return () => clearInterval(t);
  }, [load]);

  const toggleSuspend = async () => {
    if (!job) return;
    try {
      await api.suspendJob(namespace, name, !job.spec.suspend);
      load();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  const runNow = async () => {
    try {
      const run = await api.runNow(namespace, name);
      setNotice(`Run ${run.name} triggered.`);
      setTimeout(() => setNotice(""), 5000);
      setTimeout(load, 1500);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  const remove = async () => {
    if (!window.confirm(`Delete cronjob ${namespace}/${name}?`)) return;
    try {
      await api.deleteJob(namespace, name);
      navigate("/cronjobs");
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  if (error) return <div className="error-box">{error}</div>;
  if (!job) return <div className="empty">Loading…</div>;

  const manifest = stringify({
    apiVersion: "cronops.io/v1alpha1",
    kind: "HttpCronJob",
    metadata: { name: job.name, namespace: job.namespace },
    spec: job.spec,
  });

  return (
    <>
      <div className="page-head">
        <h1>
          <span className="dim">{job.namespace} / </span>
          {job.name} <PhaseBadge phase={job.status.phase} />
        </h1>
        <div className="actions" style={{ marginTop: 0 }}>
          <button onClick={runNow}>Run now</button>
          <button className="secondary" onClick={toggleSuspend}>
            {job.spec.suspend ? "Resume" : "Suspend"}
          </button>
          <button className="danger" onClick={remove}>
            Delete
          </button>
        </div>
      </div>
      {notice && <div className="info-box">{notice}</div>}

      <div className="row" style={{ alignItems: "flex-start" }}>
        <div className="panel" style={{ marginTop: 0 }}>
          <h2 style={{ marginTop: 0 }}>Summary</h2>
          <dl className="detail-grid">
            <dt>Project</dt>
            <dd>{job.spec.project || "default"}</dd>
            <dt>Schedule</dt>
            <dd className="mono">
              {job.spec.schedule}
              {job.spec.timezone ? ` (${job.spec.timezone})` : ""}
            </dd>
            <dt>Endpoint</dt>
            <dd className="mono">
              {job.spec.method} {job.spec.endpoint}
            </dd>
            <dt>Auth</dt>
            <dd>{job.spec.auth?.type ?? "none"}</dd>
            <dt>Concurrency</dt>
            <dd>{job.spec.concurrencyPolicy ?? "Forbid"}</dd>
            <dt>Timeout</dt>
            <dd>{job.spec.timeoutSeconds ?? 30}s</dd>
            <dt>Retry</dt>
            <dd>
              {job.spec.retry?.maxAttempts && job.spec.retry.maxAttempts > 1
                ? `${job.spec.retry.maxAttempts} attempts, backoff ${job.spec.retry.backoffSeconds ?? 10}s`
                : "off"}
            </dd>
            {job.spec.successCriteria && (
              <>
                <dt>Body criteria</dt>
                <dd className="mono">
                  {job.spec.successCriteria.bodyRegex
                    ? `regex: ${job.spec.successCriteria.bodyRegex}`
                    : `${job.spec.successCriteria.jsonPath}${job.spec.successCriteria.value ? ` == ${job.spec.successCriteria.value}` : ""}`}
                </dd>
              </>
            )}
            <dt>Last scheduled</dt>
            <dd>{formatTime(job.status.lastScheduleTime)}</dd>
            <dt>Next run</dt>
            <dd>{formatTime(job.status.nextScheduleTime)}</dd>
            <dt>Created</dt>
            <dd>{formatTime(job.createdAt)}</dd>
          </dl>
          {job.status.conditions?.map((c) => (
            <div key={c.type} className={c.status === "True" ? "info-box" : "error-box"} style={{ marginTop: 14, marginBottom: 0 }}>
              {c.type}: {c.reason} {c.message ? `— ${c.message}` : ""}
            </div>
          ))}
        </div>

        <div className="panel" style={{ marginTop: 0, flex: "0 0 42%" }}>
          <h2 style={{ marginTop: 0 }}>Manifest</h2>
          <pre className="yaml-preview">{manifest}</pre>
        </div>
      </div>

      <div className="panel">
        <h2 style={{ marginTop: 0 }}>Run history</h2>
        {runs.length === 0 ? (
          <div className="empty">No runs recorded yet.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Run</th>
                <th>Started</th>
                <th>Trigger</th>
                <th>Duration</th>
                <th>Attempts</th>
                <th>Result</th>
                <th>HTTP</th>
                <th>Message</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {runs.map((run) => (
                <Fragment key={run.name}>
                  <tr
                    className="run-row"
                    onClick={() => setExpandedRun(expandedRun === run.name ? null : run.name)}
                  >
                    <td className="mono dim">{run.name}</td>
                    <td>{formatTime(run.startedAt ?? run.createdAt)}</td>
                    <td className="dim">{run.trigger}</td>
                    <td className="dim">
                      {run.durationMs != null && run.durationMs > 0 ? `${run.durationMs} ms` : "—"}
                    </td>
                    <td className="dim">{run.attempts || "—"}</td>
                    <td>
                      {run.phase === "Succeeded" ? (
                        <ResultBadge result="Success" />
                      ) : run.phase === "Failed" ? (
                        <ResultBadge result="Failed" />
                      ) : (
                        <span className="badge">{run.phase}</span>
                      )}
                    </td>
                    <td className="mono">{run.httpStatusCode || "—"}</td>
                    <td className="dim">{run.message || ""}</td>
                    <td className="dim expand-hint">{expandedRun === run.name ? "▲" : "▼"}</td>
                  </tr>
                  {expandedRun === run.name && (
                    <tr className="run-detail">
                      <td colSpan={9}>
                        {run.message && (
                          <div className="run-detail-block">
                            <div className="dim">Message</div>
                            <pre className="yaml-preview">{run.message}</pre>
                          </div>
                        )}
                        <div className="run-detail-block">
                          <div className="dim">Response body (truncated to 2 KiB)</div>
                          {run.responseBody ? (
                            <pre className="yaml-preview">{run.responseBody}</pre>
                          ) : (
                            <div className="dim" style={{ padding: "8px 0" }}>
                              {job.spec.captureResponseBody === false
                                ? "Response body capture is disabled for this job (spec.captureResponseBody: false)."
                                : "Empty response body."}
                            </div>
                          )}
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
