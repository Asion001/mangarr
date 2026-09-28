import { useLocale } from "./lib/uiPreferences";
import { lazy, useEffect, useRef } from "react";
import { Navigate, Route, Routes, useMatch } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Layout } from "./components/Layout";
import { Loading } from "./components/ui";
import { useAuthStatus } from "./api/queries";
import { onServerEvent, useLiveUpdates } from "./lib/events";
import { useToast } from "./lib/toast";
import { createEventToastController } from "./lib/eventToasts";
import { LoginPage } from "./pages/auth/Login";
import { InvitePage } from "./pages/auth/Invite";
import { Need } from "./components/Need";
import { RouteBoundary } from "./components/RouteBoundary";

// Pages load on first visit, so the first screen doesn't wait for every other
// page's code. The reader gets its own chunk.
const ReaderPage = lazy(() => import("./pages/reader/Reader").then((m) => ({ default: m.ReaderPage })));
const RequestsPage = lazy(() => import("./pages/requests/Requests").then((m) => ({ default: m.RequestsPage })));
const AccountPage = lazy(() => import("./pages/account/Account").then((m) => ({ default: m.AccountPage })));
const UsersPage = lazy(() => import("./pages/settings/Users").then((m) => ({ default: m.UsersPage })));
const SingleSignOnPage = lazy(() => import("./pages/settings/SingleSignOn").then((m) => ({ default: m.SingleSignOnPage })));
const SeriesIndex = lazy(() => import("./pages/series/SeriesIndex").then((m) => ({ default: m.SeriesIndex })));
const SeriesDetail = lazy(() => import("./pages/series/SeriesDetail").then((m) => ({ default: m.SeriesDetail })));
const AddOptionsRedirect = lazy(() => import("./pages/series/AddSeries").then((m) => ({ default: m.AddOptionsRedirect })));
const AddReviewStep = lazy(() => import("./pages/series/AddSeries").then((m) => ({ default: m.AddReviewStep })));
const AddSearchStep = lazy(() => import("./pages/series/AddSeries").then((m) => ({ default: m.AddSearchStep })));
const QueuePage = lazy(() => import("./pages/activity/Queue").then((m) => ({ default: m.QueuePage })));
const HistoryPage = lazy(() => import("./pages/activity/History").then((m) => ({ default: m.HistoryPage })));
const BlocklistPage = lazy(() => import("./pages/activity/Blocklist").then((m) => ({ default: m.BlocklistPage })));
const WantedPage = lazy(() => import("./pages/activity/Wanted").then((m) => ({ default: m.WantedPage })));
const SourcesPage = lazy(() => import("./pages/sources/Sources").then((m) => ({ default: m.SourcesPage })));
const SearchSettingsPage = lazy(() => import("./pages/settings/SearchSettings").then((m) => ({ default: m.SearchSettingsPage })));
const SchedulePage = lazy(() => import("./pages/settings/Schedule").then((m) => ({ default: m.SchedulePage })));
const CleanupPage = lazy(() => import("./pages/settings/Cleanup").then((m) => ({ default: m.CleanupPage })));
const ModulesPage = lazy(() => import("./pages/settings/Modules").then((m) => ({ default: m.ModulesPage })));
const ProfilesPage = lazy(() => import("./pages/settings/Profiles").then((m) => ({ default: m.ProfilesPage })));
const MediaPage = lazy(() => import("./pages/settings/Media").then((m) => ({ default: m.MediaPage })));
const ReadersPage = lazy(() => import("./pages/settings/Readers").then((m) => ({ default: m.ReadersPage })));
const ReadingAppsPage = lazy(() => import("./pages/settings/ReadingApps").then((m) => ({ default: m.ReadingAppsPage })));
const GeneralPage = lazy(() => import("./pages/settings/General").then((m) => ({ default: m.GeneralPage })));
const DownloadsPage = lazy(() => import("./pages/settings/Downloads").then((m) => ({ default: m.DownloadsPage })));
const StatusPage = lazy(() => import("./pages/system/Status").then((m) => ({ default: m.StatusPage })));
const TasksPage = lazy(() => import("./pages/system/Tasks").then((m) => ({ default: m.TasksPage })));
const BackupsPage = lazy(() => import("./pages/system/Backups").then((m) => ({ default: m.BackupsPage })));
const LogsPage = lazy(() => import("./pages/system/Logs").then((m) => ({ default: m.LogsPage })));
const DatabasePage = lazy(() => import("./pages/system/Database").then((m) => ({ default: m.DatabasePage })));
const WorkersPage = lazy(() => import("./pages/system/Workers").then((m) => ({ default: m.WorkersPage })));
const RecycleBinPage = lazy(() => import("./pages/system/RecycleBin").then((m) => ({ default: m.RecycleBinPage })));
const RecycledReaderPage = lazy(() => import("./pages/system/RecycledReader").then((m) => ({ default: m.RecycledReaderPage })));
const ImportsPage = lazy(() => import("./pages/import/Imports").then((m) => ({ default: m.ImportsPage })));
const ImportDetailPage = lazy(() => import("./pages/import/ImportDetail").then((m) => ({ default: m.ImportDetailPage })));
const UpdatesPage = lazy(() => import("./pages/updates/Updates").then((m) => ({ default: m.UpdatesPage })));
const DiscoverShelfPage = lazy(() => import("./pages/discover/DiscoverShelf").then((m) => ({ default: m.DiscoverShelfPage })));
const DiscoverPage = lazy(() => import("./pages/discover/Discover").then((m) => ({ default: m.DiscoverPage })));

export function App() {
  useLocale();
  const { data: auth, isLoading } = useAuthStatus();
  const qc = useQueryClient();
  const toast = useToast();
  const authed = !!auth?.authenticated;
  const invite = useMatch("/invite/:token");
  const readerOpen = !!useMatch("/read/:id");
  const readerOpenRef = useRef(readerOpen);
  const toastRef = useRef(toast);
  readerOpenRef.current = readerOpen;
  toastRef.current = toast;
  const eventToasts = useRef<ReturnType<typeof createEventToastController> | null>(null);
  if (!eventToasts.current) {
    eventToasts.current = createEventToastController({
      success: (title, message) => toastRef.current.success(title, message),
      error: (title, message) => toastRef.current.error(title, message),
      warning: (title, message) => toastRef.current.warning(title, message),
      info: (title, message) => toastRef.current.info(title, message),
    }, { suppressed: () => readerOpenRef.current });
  }
  useLiveUpdates(authed);

  useEffect(() => {
    const onUnauth = () => qc.invalidateQueries({ queryKey: ["auth"] });
    window.addEventListener("mangarr:unauthorized", onUnauth);
    return () => window.removeEventListener("mangarr:unauthorized", onUnauth);
  }, [qc]);

  useEffect(() => {
    const controller = eventToasts.current!;
    const off = onServerEvent(controller.handle);
    return () => {
      off();
      controller.discard();
    };
  }, []);

  if (invite) return <InvitePage token={invite.params.token ?? ""} />;
  if (isLoading) return <Loading />;
  if (!authed) return <LoginPage setup={!!auth?.needsSetup} />;

  return (
    <RouteBoundary>
      <Routes>
        <Route path="read/:id" element={<ReaderPage />} />
        <Route path="recycle-bin/:id/read" element={<Need perm="admin"><RecycledReaderPage /></Need>} />
        <Route element={<Layout />}>
          <Route index element={<SeriesIndex />} />
          <Route path="discover" element={<DiscoverPage />} />
          <Route path="discover/:shelf" element={<DiscoverShelfPage />} />
          <Route path="series/:id" element={<SeriesDetail />} />
          <Route path="updates" element={<UpdatesPage />} />
          <Route path="account" element={<AccountPage />} />
          <Route path="add" element={<Need perm={["library.manage", "requests.manage"]}><AddSearchStep /></Need>} />
          <Route path="add/:moduleId/:metaId/sources" element={<Need perm={["library.manage", "requests.manage"]}><AddReviewStep /></Need>} />
          <Route path="add/:moduleId/:metaId/options" element={<Need perm={["library.manage", "requests.manage"]}><AddOptionsRedirect /></Need>} />
          <Route path="requests" element={<Need perm={["requests.create", "requests.manage", "library.manage"]}><RequestsPage /></Need>} />
          <Route path="import" element={<Need perm="admin"><ImportsPage /></Need>} />
          <Route path="import/:id" element={<Need perm="admin"><ImportDetailPage /></Need>} />
          <Route path="activity" element={<Navigate to="/activity/downloads" replace />} />
          <Route path="activity/queue" element={<Navigate to="/activity/downloads" replace />} />
          <Route path="activity/downloads" element={<Need perm="library.manage"><QueuePage mode="downloads" /></Need>} />
          <Route path="activity/processing" element={<Need perm="library.manage"><QueuePage mode="processing" /></Need>} />
          <Route path="activity/history" element={<Need perm="library.manage"><HistoryPage /></Need>} />
          <Route path="activity/blocklist" element={<Need perm="library.manage"><BlocklistPage /></Need>} />
          <Route path="wanted" element={<Need perm="library.manage"><WantedPage /></Need>} />
          <Route path="sources" element={<Need perm="library.manage"><SourcesPage /></Need>} />
          <Route path="sources/:tab" element={<Need perm="library.manage"><SourcesPage /></Need>} />
          <Route path="cleanup" element={<Need perm="admin"><CleanupPage /></Need>} />
          <Route path="settings" element={<Navigate to="/settings/media" replace />} />
          <Route path="settings/media" element={<Need perm="admin"><MediaPage /></Need>} />
          <Route path="settings/profiles" element={<Need perm="admin"><ProfilesPage /></Need>} />
          <Route path="settings/sources" element={<Need perm="admin"><ModulesPage kind="source" /></Need>} />
          <Route path="settings/search" element={<Need perm="admin"><SearchSettingsPage /></Need>} />
          <Route path="settings/schedule" element={<Need perm="admin"><SchedulePage /></Need>} />
          <Route path="settings/metadata" element={<Need perm="admin"><ModulesPage kind="metadata" /></Need>} />
          <Route path="settings/library" element={<Need perm="admin"><ModulesPage kind="library" /></Need>} />
          <Route path="settings/notifications" element={<Need perm="admin"><ModulesPage kind="notify" /></Need>} />
          <Route path="settings/media-servers" element={<Need perm="admin"><ModulesPage kind="mediaserver" /></Need>} />
          <Route path="settings/upscalers" element={<Navigate to="/system/workers" replace />} />
          <Route path="settings/sso" element={<Need perm="admin"><SingleSignOnPage /></Need>} />
          <Route path="settings/users" element={<Need perm="admin"><UsersPage /></Need>} />
          <Route path="settings/readers" element={<Need perm="admin"><ReadersPage /></Need>} />
          <Route path="settings/reading" element={<Need perm="admin"><ReadingAppsPage /></Need>} />
          <Route path="settings/downloads" element={<Need perm="admin"><DownloadsPage /></Need>} />
          <Route path="settings/general" element={<Need perm="admin"><GeneralPage /></Need>} />
          <Route path="system" element={<Navigate to="/system/status" replace />} />
          <Route path="system/status" element={<Need perm="admin"><StatusPage /></Need>} />
          <Route path="system/tasks" element={<Need perm="admin"><TasksPage /></Need>} />
          <Route path="system/workers" element={<Need perm="admin"><WorkersPage /></Need>} />
          <Route path="system/backups" element={<Need perm="admin"><BackupsPage /></Need>} />
          <Route path="system/recycle-bin" element={<Need perm="admin"><RecycleBinPage /></Need>} />
          <Route path="system/logs" element={<Need perm="admin"><LogsPage /></Need>} />
          <Route path="system/database" element={<Need perm="admin"><DatabasePage /></Need>} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </RouteBoundary>
  );
}
