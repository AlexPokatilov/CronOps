import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth";
import { Layout } from "./components/Layout";
import { LoginPage } from "./pages/Login";
import { DashboardPage } from "./pages/Dashboard";
import { CronJobsPage } from "./pages/CronJobs";
import { CronJobNewPage } from "./pages/CronJobNew";
import { CronJobDetailPage } from "./pages/CronJobDetail";
import { ProjectsPage } from "./pages/Projects";

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();
  const location = useLocation();
  if (loading) {
    return <div className="empty">Loading…</div>;
  }
  if (!user) {
    return <Navigate to="/login" state={{ from: location }} replace />;
  }
  return <>{children}</>;
}

export default function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          element={
            <RequireAuth>
              <Layout />
            </RequireAuth>
          }
        >
          <Route path="/" element={<Navigate to="/dashboard" replace />} />
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/cronjobs" element={<CronJobsPage />} />
          <Route path="/cronjobs/new" element={<CronJobNewPage />} />
          <Route path="/cronjobs/:namespace/:name" element={<CronJobDetailPage />} />
          <Route path="/projects" element={<ProjectsPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </AuthProvider>
  );
}
