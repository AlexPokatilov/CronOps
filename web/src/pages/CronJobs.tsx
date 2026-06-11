import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api";
import type { JobView, ProjectView } from "../types";
import { PhaseBadge, ResultBadge, formatTime } from "../components/badges";

export function CronJobsPage() {
  const [jobs, setJobs] = useState<JobView[] | null>(null);
  const [projects, setProjects] = useState<ProjectView[]>([]);
  const [searchParams, setSearchParams] = useSearchParams();
  const project = searchParams.get("project") ?? "";
  const setProject = (p: string) => setSearchParams(p ? { project: p } : {});
  const [error, setError] = useState("");
  const navigate = useNavigate();

  const load = useCallback(() => {
    api
      .listJobs(undefined, project || undefined)
      .then((res) => setJobs(res.items))
      .catch((e) => setError(String(e.message ?? e)));
  }, [project]);

  useEffect(() => {
    api
      .listProjects()
      .then((res) => setProjects(res.items))
      .catch(() => {});
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

  const runNow = async (job: JobView) => {
    try {
      await api.runNow(job.namespace, job.name);
      setTimeout(load, 1500);
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
        <div className="actions" style={{ marginTop: 0 }}>
          <select value={project} onChange={(e) => setProject(e.target.value)}>
            <option value="">All projects</option>
            {projects.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name} ({p.jobCount})
              </option>
            ))}
          </select>
          <button onClick={() => navigate("/cronjobs/new")}>+ New CronJob</button>
        </div>
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
                <th>Project</th>
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
                  <td className="dim">{job.spec.project || "default"}</td>
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
                    <button className="secondary small" onClick={() => runNow(job)}>
                      Run now
                    </button>{" "}
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
