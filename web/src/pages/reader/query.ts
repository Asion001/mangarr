import { api, unwrap } from "../../api/client";
import { rememberPageBase } from "./page";

/** chapterQuery is a chapter as the reader opens it. */
export const chapterQuery = (id: number) => ({
  queryKey: ["read-chapter", id],
  queryFn: () => unwrap(api.GET("/api/v1/read/chapters/{id}", { params: { path: { id } } })).then((ch) => {
    rememberPageBase(ch.id, ch.pageBase);
    return ch;
  }),
  staleTime: 60_000,
});
