import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useAuth } from "../auth";

export function Layout() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();

  const onLogout = async () => {
    await logout();
    navigate("/login");
  };

  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="logo">
          Cron<span>Ops</span>
        </div>
        <nav>
          <NavLink to="/dashboard">Dashboard</NavLink>
          <NavLink to="/cronjobs" end>
            CronJobs
          </NavLink>
          <NavLink to="/cronjobs/new">New CronJob</NavLink>
          <NavLink to="/projects">Projects</NavLink>
        </nav>
        <div className="spacer" />
        <div className="user">
          <span>{user}</span>
          <button className="link" onClick={onLogout}>
            Logout
          </button>
        </div>
      </aside>
      <main className="content">
        <Outlet />
      </main>
    </div>
  );
}
