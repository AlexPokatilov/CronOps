import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import type { Stats } from "../types";
import { ResultBadge, formatTime } from "../components/badges";

export function DashboardPage() {
  const [stats, setStats] = useState<Stats | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .stats()
        .then((s) => !cancelled && setStats(s))
        .catch((e) => !cancelled && setError(String(e.message ?? e)));
    load();
    const t = setInterval(load, 10_000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, []);

  if (error) return <div className="error-box">{error}</div>;
  if (!stats) return <div className="empty">Loading…</div>;

  return (
    <>
      <h1>Dashboard</h1>
      <div className="cards">
        <div className="card">
          <div className="label">Total cronjobs</div>
          <div className="value">{stats.total}</div>
        </div>
        <div className="card success">
          <div className="label">Active</div>
          <div className="value">{stats.active}</div>
        </div>
        <div className="card warning">
          <div className="label">Suspended</div>
          <div className="value">{stats.suspended}</div>
        </div>
        <div className="card danger">
          <div className="label">Invalid</div>
          <div className="value">{stats.invalid}</div>
        </div>
        <div className="card success">
          <div className="label">Last runs · success</div>
          <div className="value">{stats.lastRuns.success}</div>
        </div>
        <div className="card danger">
          <div className="label">Last runs · failed</div>
          <div className="value">{stats.lastRuns.failed}</div>
        </div>
      </div>

      <div className="panel">
        <h2 style={{ marginTop: 0 }}>Recent runs</h2>
        {stats.recentRuns.length === 0 ? (
          <div className="empty">
            No runs yet. <Link to="/cronjobs/new">Create your first cronjob</Link>.
          </div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Job</th>
                <th>Namespace</th>
                <th>Started</th>
                <th>Result</th>
                <th>HTTP</th>
                <th>Message</th>
              </tr>
            </thead>
            <tbody>
              {stats.recentRuns.map((run, i) => (
                <tr key={i}>
                  <td>
                    <Link to={`/cronjobs/${run.namespace}/${run.job}`}>{run.job}</Link>
                  </td>
                  <td className="dim">{run.namespace}</td>
                  <td>{formatTime(run.startedAt)}</td>
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
