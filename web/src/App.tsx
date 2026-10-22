import { Suspense, lazy } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { Spin } from "antd";
import { useAuth } from "./auth";
import AppLayout from "./components/AppLayout";
import LoginPage from "./pages/Login";

// 标注页/审核页/统计/管理页按需加载，首屏只带登录与外壳
const WorkbenchPage = lazy(() => import("./pages/Workbench"));
const AnnotatePage = lazy(() => import("./pages/Annotate"));
const ReviewQueuePage = lazy(() => import("./pages/ReviewQueue"));
const ReviewPage = lazy(() => import("./pages/ReviewPage"));
const StatsPage = lazy(() => import("./pages/Stats"));
const UploadPage = lazy(() => import("./pages/admin/Upload"));
const FoldersPage = lazy(() => import("./pages/admin/Folders"));
const UsersPage = lazy(() => import("./pages/admin/Users"));
const ExportsPage = lazy(() => import("./pages/admin/Exports"));
const SettingsPage = lazy(() => import("./pages/admin/Settings"));

function Loading() {
  return (
    <div style={{ display: "flex", justifyContent: "center", padding: 80 }}>
      <Spin size="large" />
    </div>
  );
}

export default function Root() {
  const { user, ready, me } = useAuth();

  if (!ready) {
    return (
      <div className="boot">
        <Spin size="large" />
      </div>
    );
  }
  if (!user) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }
  if (me?.user.must_change_pwd) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="*" element={<Navigate to="/login?change=1" replace />} />
      </Routes>
    );
  }

  const home = user.role === "annotator" ? "/workbench" : user.role === "reviewer" ? "/review" : "/stats";

  return (
    <Suspense fallback={<Loading />}>
      <Routes>
        <Route element={<AppLayout />}>
          <Route path="/" element={<Navigate to={home} replace />} />
          <Route path="/workbench" element={<WorkbenchPage />} />
          <Route path="/workbench/:imageId" element={<AnnotatePage />} />
          <Route path="/review" element={<ReviewQueuePage />} />
          <Route path="/review/:imageId" element={<ReviewPage />} />
          <Route path="/stats" element={<StatsPage />} />
          <Route path="/admin/upload" element={<UploadPage />} />
          <Route path="/admin/folders" element={<FoldersPage />} />
          <Route path="/admin/users" element={<UsersPage />} />
          <Route path="/admin/exports" element={<ExportsPage />} />
          <Route path="/admin/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to={home} replace />} />
        </Route>
      </Routes>
    </Suspense>
  );
}
