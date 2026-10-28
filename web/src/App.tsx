import { useLocale } from "./lib/uiPreferences";
import { lazy, useEffect, useRef } from "react";
import { Navigate, Route, Routes, useMatch } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Layout, SettingsHome } from "./components/Layout";
import { Loading } from "./components/ui";
import { useAuthStatus } from "./api/queries";
import { onServerEvent, useLiveUpdates } from "./lib/events";
import { useToast } from "./lib/toast";
import { createEventToastController } from "./lib/eventToasts";
import { createUpdateNotice } from "./lib/updateNotice";
import { t } from "./lib/i18n/core";
import { RefreshCw } from "lucide-react";
import { LoginPage } from "./pages/auth/Login";
import { InvitePage } from "./pages/auth/Invite";
import { Need } from "./components/Need";
import { retryWhenBack, RouteBoundary } from "./components/RouteBoundary";

// Pages load on first visit, so the first screen doesn't wait for every other
// page's code. The reader gets its own chunk. One asked for while mangarr
// restarts loads once it's back.
const ReaderPage = lazy(() => retryWhenBack(() => import("./pages/reader/Reader"), (m) => ({ default: m.ReaderPage })));
const RequestsPage = lazy(() => retryWhenBack(() => import("./pages/requests/Requests"), (m) => ({ default: m.RequestsPage })));
const OnDevicePage = lazy(() => retryWhenBack(() => import("./pages/account/OnDevice"), (m) => ({ default: m.OnDevicePage })));
const AccountPage = lazy(() => retryWhenBack(() => import("./pages/account/Account"), (m) => ({ default: m.AccountPage })));
const UsersPage = lazy(() => retryWhenBack(() => import("./pages/settings/Users"), (m) => ({ default: m.UsersPage })));
const SingleSignOnPage = lazy(() => retryWhenBack(() => import("./pages/settings/SingleSignOn"), (m) => ({ default: m.SingleSignOnPage })));
const SeriesIndex = lazy(() => retryWhenBack(() => import("./pages/series/SeriesIndex"), (m) => ({ default: m.SeriesIndex })));
const SeriesDetail = lazy(() => retryWhenBack(() => import("./pages/series/SeriesDetail"), (m) => ({ default: m.SeriesDetail })));
const AddOptionsRedirect = lazy(() => retryWhenBack(() => import("./pages/series/AddSeries"), (m) => ({ default: m.AddOptionsRedirect })));
const AddReviewStep = lazy(() => retryWhenBack(() => import("./pages/series/AddSeries"), (m) => ({ default: m.AddReviewStep })));
const AddSearchStep = lazy(() => retryWhenBack(() => import("./pages/series/AddSeries"), (m) => ({ default: m.AddSearchStep })));
const QueuePage = lazy(() => retryWhenBack(() => import("./pages/activity/Queue"), (m) => ({ default: m.QueuePage })));
const HistoryPage = lazy(() => retryWhenBack(() => import("./pages/activity/History"), (m) => ({ default: m.HistoryPage })));
const BlocklistPage = lazy(() => retryWhenBack(() => import("./pages/activity/Blocklist"), (m) => ({ default: m.BlocklistPage })));
const WantedPage = lazy(() => retryWhenBack(() => import("./pages/activity/Wanted"), (m) => ({ default: m.WantedPage })));
const SourcesPage = lazy(() => retryWhenBack(() => import("./pages/sources/Sources"), (m) => ({ default: m.SourcesPage })));
const SearchSettingsPage = lazy(() => retryWhenBack(() => import("./pages/settings/SearchSettings"), (m) => ({ default: m.SearchSettingsPage })));
const SchedulePage = lazy(() => retryWhenBack(() => import("./pages/settings/Schedule"), (m) => ({ default: m.SchedulePage })));
const DuplicatesPage = lazy(() => retryWhenBack(() => import("./pages/duplicates/Duplicates"), (m) => ({ default: m.DuplicatesPage })));
const CleanupPage = lazy(() => retryWhenBack(() => import("./pages/settings/Cleanup"), (m) => ({ default: m.CleanupPage })));
const ModulesPage = lazy(() => retryWhenBack(() => import("./pages/settings/Modules"), (m) => ({ default: m.ModulesPage })));
const ProfilesPage = lazy(() => retryWhenBack(() => import("./pages/settings/Profiles"), (m) => ({ default: m.ProfilesPage })));
const MediaPage = lazy(() => retryWhenBack(() => import("./pages/settings/Media"), (m) => ({ default: m.MediaPage })));
const ReadingAppsPage = lazy(() => retryWhenBack(() => import("./pages/settings/ReadingApps"), (m) => ({ default: m.ReadingAppsPage })));
const GeneralPage = lazy(() => retryWhenBack(() => import("./pages/settings/General"), (m) => ({ default: m.GeneralPage })));
const DownloadsPage = lazy(() => retryWhenBack(() => import("./pages/settings/Downloads"), (m) => ({ default: m.DownloadsPage })));
const PerformancePage = lazy(() => retryWhenBack(() => import("./pages/system/Performance"), (m) => ({ default: m.PerformancePage })));
const StatusPage = lazy(() => retryWhenBack(() => import("./pages/system/Status"), (m) => ({ default: m.StatusPage })));
const TasksPage = lazy(() => retryWhenBack(() => import("./pages/system/Tasks"), (m) => ({ default: m.TasksPage })));
const BackupsPage = lazy(() => retryWhenBack(() => import("./pages/system/Backups"), (m) => ({ default: m.BackupsPage })));
const LogsPage = lazy(() => retryWhenBack(() => import("./pages/system/Logs"), (m) => ({ default: m.LogsPage })));
const StoragePage = lazy(() => retryWhenBack(() => import("./pages/system/Storage"), (m) => ({ default: m.StoragePage })));
const DatabasePage = lazy(() => retryWhenBack(() => import("./pages/system/Database"), (m) => ({ default: m.DatabasePage })));
const WorkersPage = lazy(() => retryWhenBack(() => import("./pages/system/Workers"), (m) => ({ default: m.WorkersPage })));
const RecycleBinPage = lazy(() => retryWhenBack(() => import("./pages/system/RecycleBin"), (m) => ({ default: m.RecycleBinPage })));
const RecycledReaderPage = lazy(() => retryWhenBack(() => import("./pages/system/RecycledReader"), (m) => ({ default: m.RecycledReaderPage })));
const ImportsPage = lazy(() => retryWhenBack(() => import("./pages/import/Imports"), (m) => ({ default: m.ImportsPage })));
const ImportDetailPage = lazy(() => retryWhenBack(() => import("./pages/import/ImportDetail"), (m) => ({ default: m.ImportDetailPage })));
const UpdatesPage = lazy(() => retryWhenBack(() => import("./pages/updates/Updates"), (m) => ({ default: m.UpdatesPage })));
const DiscoverShelfPage = lazy(() => retryWhenBack(() => import("./pages/discover/DiscoverShelf"), (m) => ({ default: m.DiscoverShelfPage })));
const DiscoverPage = lazy(() => retryWhenBack(() => import("./pages/discover/Discover"), (m) => ({ default: m.DiscoverPage })));

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
  const updateNotice = useRef<ReturnType<typeof createUpdateNotice> | null>(null);
  if (!updateNotice.current) {
    updateNotice.current = createUpdateNotice({
      show: () =>
        toastRef.current.action("info", t("mangarr was updated"), t("Reload to get the new version."), {
          label: t("Update"),
          icon: <RefreshCw className="size-4" />,
          onClick: () => window.location.reload(),
        }),
      suppressed: () => readerOpenRef.current,
    });
  }
  // a notice held back while reading shows once the reader closes
  useEffect(() => {
    if (!readerOpen) updateNotice.current!.flush();
  }, [readerOpen]);
  useLiveUpdates(authed);

  useEffect(() => {
    const onUnauth = () => qc.invalidateQueries({ queryKey: ["auth"] });
    window.addEventListener("mangarr:unauthorized", onUnauth);
    return () => window.removeEventListener("mangarr:unauthorized", onUnauth);
  }, [qc]);

  useEffect(() => {
    const controller = eventToasts.current!;
    const off = onServerEvent((type, payload) => {
      if (type === "hello") updateNotice.current!.seen((payload as { build?: string } | undefined)?.build);
      else controller.handle(type, payload);
    });
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
          <Route path="on-device" element={<OnDevicePage />} />
          <Route path="account/:section" element={<AccountPage />} />
          <Route path="add" element={<Need perm={["library.add", "requests.manage"]}><AddSearchStep /></Need>} />
          <Route path="add/:moduleId/:metaId/sources" element={<Need perm={["library.add", "requests.manage"]}><AddReviewStep /></Need>} />
          <Route path="add/:moduleId/:metaId/options" element={<Need perm={["library.add", "requests.manage"]}><AddOptionsRedirect /></Need>} />
          <Route path="requests" element={<Need perm={["requests.create", "requests.manage", "library.manage"]}><RequestsPage /></Need>} />
          <Route path="import" element={<Need perm="admin"><ImportsPage /></Need>} />
          <Route path="import/:id" element={<Need perm="admin"><ImportDetailPage /></Need>} />
          <Route path="activity" element={<Navigate to="/activity/downloads" replace />} />
          <Route path="activity/queue" element={<Navigate to="/activity/downloads" replace />} />
          <Route path="activity/downloads" element={<Need perm="activity.view"><QueuePage mode="downloads" /></Need>} />
          <Route path="activity/processing" element={<Need perm="activity.view"><QueuePage mode="processing" /></Need>} />
          <Route path="activity/history" element={<Need perm="queue.manage"><HistoryPage /></Need>} />
          <Route path="activity/blocklist" element={<Need perm="queue.manage"><BlocklistPage /></Need>} />
          <Route path="wanted" element={<Need perm="library.edit"><WantedPage /></Need>} />
          <Route path="sources" element={<Need perm="library.edit"><SourcesPage /></Need>} />
          <Route path="sources/:tab" element={<Need perm="library.edit"><SourcesPage /></Need>} />
          <Route path="cleanup" element={<Need perm="admin"><CleanupPage /></Need>} />
          <Route path="duplicates" element={<Need perm="admin"><DuplicatesPage /></Need>} />
          <Route path="settings" element={<Need perm="admin"><SettingsHome /></Need>} />
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
          <Route path="settings/readers" element={<Navigate to="/settings/users" replace />} />
          <Route path="settings/reading" element={<Need perm="admin"><ReadingAppsPage /></Need>} />
          <Route path="settings/downloads" element={<Need perm="admin"><DownloadsPage /></Need>} />
          <Route path="settings/general" element={<Need perm="admin"><GeneralPage /></Need>} />
          <Route path="system" element={<Navigate to="/system/status" replace />} />
          <Route path="system/status" element={<Need perm="admin"><StatusPage /></Need>} />
          <Route path="system/performance" element={<Need perm="admin"><PerformancePage /></Need>} />
          <Route path="system/tasks" element={<Need perm="admin"><TasksPage /></Need>} />
          <Route path="system/workers" element={<Need perm="admin"><WorkersPage /></Need>} />
          <Route path="system/backups" element={<Need perm="admin"><BackupsPage /></Need>} />
          <Route path="system/recycle-bin" element={<Need perm="admin"><RecycleBinPage /></Need>} />
          <Route path="system/logs" element={<Need perm="admin"><LogsPage /></Need>} />
          <Route path="system/storage" element={<Need perm="admin"><StoragePage /></Need>} />
          <Route path="system/database" element={<Need perm="admin"><DatabasePage /></Need>} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </RouteBoundary>
  );
}
