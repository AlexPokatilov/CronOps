import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import type { ProjectView } from "../types";
import { formatTime } from "../components/badges";

export function ProjectsPage() {
  const [projects, setProjects] = useState<ProjectView[] | null>(null);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  const load = useCallback(() => {
    api
      .listProjects()
      .then((res) => setProjects(res.items))
      .catch((e) => setError(String(e.message ?? e)));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await api.createProject(name.trim(), description.trim());
      setName("");
      setDescription("");
      load();
    } catch (err) {
      setError(String((err as Error).message ?? err));
    }
  };

  const remove = async (project: ProjectView) => {
    if (!window.confirm(`Delete project ${project.name}? Jobs keep their spec.project reference.`)) return;
    try {
      await api.deleteProject(project.name);
      load();
    } catch (err) {
      setError(String((err as Error).message ?? err));
    }
  };

  return (
    <>
      <div className="page-head">
        <h1>Projects</h1>
      </div>
      {error && <div className="error-box">{error}</div>}

      <div className="panel">
        <form onSubmit={create} className="actions" style={{ marginTop: 0, alignItems: "flex-end" }}>
          <label style={{ flex: "0 0 220px" }}>
            Name
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="payments"
              pattern="[a-z0-9]([\-a-z0-9]*[a-z0-9])?"
              title="lowercase letters, digits and dashes (a valid Kubernetes name)"
              required
            />
          </label>
          <label style={{ flex: 1 }}>
            Description
            <input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Payment-team cron endpoints"
            />
          </label>
          <button type="submit">+ Create project</button>
        </form>
      </div>

      <div className="panel">
        {!projects ? (
          <div className="empty">Loading…</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Description</th>
                <th>Jobs</th>
                <th>Created</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {projects.map((p) => (
                <tr key={p.name}>
                  <td>
                    <Link to={`/cronjobs?project=${encodeURIComponent(p.name)}`}>{p.name}</Link>
                  </td>
                  <td className="dim">{p.description || "—"}</td>
                  <td>{p.jobCount}</td>
                  <td className="dim">{p.createdAt ? formatTime(p.createdAt) : "—"}</td>
                  <td style={{ whiteSpace: "nowrap" }}>
                    {p.name !== "default" && (
                      <button className="danger small" onClick={() => remove(p)}>
                        Delete
                      </button>
                    )}
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
