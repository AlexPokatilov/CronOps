import { useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { stringify } from "yaml";
import { api } from "../api";
import type { JobView } from "../types";
import { PhaseBadge, ResultBadge, formatTime } from "../components/badges";

export function CronJobDetailPage() {
  const { namespace = "", name = "" } = useParams();
  const [job, setJob] = useState<JobView | null>(null);
  const [error, setError] = useState("");
  const navigate = useNavigate();

  const load = useCallback(() => {
    api
      .getJob(namespace, name)
      .then(setJob)
      .catch((e) => setError(String(e.message ?? e)));
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
          <button className="secondary" onClick={toggleSuspend}>
            {job.spec.suspend ? "Resume" : "Suspend"}
          </button>
          <button className="danger" onClick={remove}>
            Delete
          </button>
        </div>
      </div>

      <div className="row" style={{ alignItems: "flex-start" }}>
        <div className="panel" style={{ marginTop: 0 }}>
          <h2 style={{ marginTop: 0 }}>Summary</h2>
          <dl className="detail-grid">
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
        {!job.status.history || job.status.history.length === 0 ? (
          <div className="empty">No runs recorded yet.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Started</th>
                <th>Finished</th>
                <th>Result</th>
                <th>HTTP</th>
                <th>Message</th>
              </tr>
            </thead>
            <tbody>
              {job.status.history.map((run, i) => (
                <tr key={i}>
                  <td>{formatTime(run.startedAt)}</td>
                  <td>{formatTime(run.finishedAt)}</td>
                  <td>
                    <ResultBadge result={run.result} />
                  </td>
                  <td className="mono">{run.httpStatusCode || "—"}</td>
                  <td className="dim">{run.message || ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
