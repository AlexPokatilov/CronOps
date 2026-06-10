import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api } from "../api";
import type { JobView } from "../types";
import { PhaseBadge, ResultBadge, formatTime } from "../components/badges";

export function CronJobsPage() {
  const [jobs, setJobs] = useState<JobView[] | null>(null);
  const [error, setError] = useState("");
  const navigate = useNavigate();

  const load = useCallback(() => {
    api
      .listJobs()
      .then((res) => setJobs(res.items))
      .catch((e) => setError(String(e.message ?? e)));
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(load, 10_000);
    return () => clearInterval(t);
  }, [load]);

  const toggleSuspend = async (job: JobView) => {
    try {
      await api.suspendJob(job.namespace, job.name, !job.spec.suspend);
      load();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  const remove = async (job: JobView) => {
    if (!window.confirm(`Delete cronjob ${job.namespace}/${job.name}?`)) return;
    try {
      await api.deleteJob(job.namespace, job.name);
      load();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  return (
    <>
      <div className="page-head">
        <h1>CronJobs</h1>
        <button onClick={() => navigate("/cronjobs/new")}>+ New CronJob</button>
      </div>
      {error && <div className="error-box">{error}</div>}
      <div className="panel">
        {!jobs ? (
          <div className="empty">Loading…</div>
        ) : jobs.length === 0 ? (
          <div className="empty">
            No cronjobs yet. <Link to="/cronjobs/new">Create one</Link>.
          </div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Schedule</th>
                <th>Endpoint</th>
                <th>Phase</th>
                <th>Last run</th>
                <th>Next run</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((job) => (
                <tr key={`${job.namespace}/${job.name}`}>
                  <td>
                    <Link to={`/cronjobs/${job.namespace}/${job.name}`}>{job.name}</Link>
                  </td>
                  <td className="dim">{job.namespace}</td>
                  <td className="mono">{job.spec.schedule}</td>
                  <td className="dim mono" style={{ maxWidth: 220, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {job.spec.method} {job.spec.endpoint}
                  </td>
                  <td>
                    <PhaseBadge phase={job.status.phase} />
                  </td>
                  <td>
                    <ResultBadge result={job.status.lastRun?.result} />
                  </td>
                  <td className="dim">{formatTime(job.status.nextScheduleTime)}</td>
                  <td style={{ whiteSpace: "nowrap" }}>
                    <button className="secondary small" onClick={() => toggleSuspend(job)}>
                      {job.spec.suspend ? "Resume" : "Suspend"}
                    </button>{" "}
                    <button className="danger small" onClick={() => remove(job)}>
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
