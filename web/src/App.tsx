import { useLocale } from "./lib/uiPreferences";
import { useEffect, useRef } from "react";
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
import { ReaderPage } from "./pages/reader/Reader";
import { RequestsPage } from "./pages/requests/Requests";
import { AccountPage } from "./pages/account/Account";
import { UsersPage } from "./pages/settings/Users";
import { SingleSignOnPage } from "./pages/settings/SingleSignOn";
import { Need } from "./components/Need";
import { SeriesIndex } from "./pages/series/SeriesIndex";
import { SeriesDetail } from "./pages/series/SeriesDetail";
import { AddOptionsRedirect, AddReviewStep, AddSearchStep } from "./pages/series/AddSeries";
import { QueuePage } from "./pages/activity/Queue";
import { HistoryPage } from "./pages/activity/History";
import { BlocklistPage } from "./pages/activity/Blocklist";
import { WantedPage } from "./pages/activity/Wanted";
import { SourcesPage } from "./pages/sources/Sources";
import { SearchSettingsPage } from "./pages/settings/SearchSettings";
import { SchedulePage } from "./pages/settings/Schedule";
import { CleanupPage } from "./pages/settings/Cleanup";
import { ModulesPage } from "./pages/settings/Modules";
import { ProfilesPage } from "./pages/settings/Profiles";
import { MediaPage } from "./pages/settings/Media";
import { ReadersPage } from "./pages/settings/Readers";
import { ReadingAppsPage } from "./pages/settings/ReadingApps";
import { GeneralPage } from "./pages/settings/General";
import { DownloadsPage } from "./pages/settings/Downloads";
import { StatusPage } from "./pages/system/Status";
import { TasksPage } from "./pages/system/Tasks";
import { BackupsPage } from "./pages/system/Backups";
import { LogsPage } from "./pages/system/Logs";
import { DatabasePage } from "./pages/system/Database";
import { WorkersPage } from "./pages/system/Workers";
import { RecycleBinPage } from "./pages/system/RecycleBin";
import { RecycledReaderPage } from "./pages/system/RecycledReader";
import { ImportsPage } from "./pages/import/Imports";
import { ImportDetailPage } from "./pages/import/ImportDetail";
import { UpdatesPage } from "./pages/updates/Updates";
import { DiscoverShelfPage } from "./pages/discover/DiscoverShelf";
import { DiscoverPage } from "./pages/discover/Discover";

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
  );
}
