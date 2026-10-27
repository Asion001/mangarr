import { useQuery } from "@tanstack/react-query";
import { api, unwrap, type S } from "../api/client";
import { t } from "./i18n/core";

export type ReadingStatus = "reading" | "planned" | "completed" | "onhold" | "dropped";
export type ReadingList = S["ReadingListCount"];

/** readingStatuses in the order they show. */
export const readingStatuses: readonly ReadingStatus[] = ["reading", "planned", "completed", "onhold", "dropped"];

const statusNames: Record<ReadingStatus, string> = {
  reading: "Reading",
  planned: "Planned",
  completed: "Completed",
  onhold: "On hold",
  dropped: "Dropped",
};

export const readingStatusName = (s: ReadingStatus) => t(statusNames[s] as Parameters<typeof t>[0]);

/** readingListName shows the built-in Favorites in the interface's language. */
export const readingListName = (l: { name: string; kind: string }) => (l.kind === "favorites" ? t("Favorites") : l.name);

/** useReadingLists: a user's statuses and lists with their counts (yours without userId). */
export function useReadingLists(enabled = true, userId?: number) {
  return useQuery({
    queryKey: ["me", "lists", userId ?? 0],
    queryFn: () => unwrap(userId ? api.GET("/api/v1/users/{id}/lists", { params: { path: { id: userId } } }) : api.GET("/api/v1/me/lists")),
    enabled,
  });
}
