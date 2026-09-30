package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
)

// komgaFixture is a series with four chapters: 1 read, 2 in progress (page
// 5), 3 downloaded and unread, 4 neither downloaded nor read; plus a second,
// unstarted series and a tag.
type komgaFixture struct {
	e      *testEnv
	srv    *httptest.Server
	key    string
	ser    *model.Series
	other  *model.Series
	chs    []*model.Chapter
	tag    *model.Tag
	reader int64
}

func newKomgaFixture(t *testing.T, dsn string) *komgaFixture {
	t.Helper()
	e := newTestApp(t, dsn)
	now := time.Now().UTC().Truncate(time.Second)
	tag := &model.Tag{Label: "favourites"}
	if _, err := e.App.DB.NewInsert().Model(tag).Exec(e.Ctx); err != nil {
		t.Fatal(err)
	}
	ser := &model.Series{Title: "Blue Period", SortTitle: "blue period", Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll,
		RootFolderID: e.RFID, Path: "Blue Period", ProfileID: 1, Tags: []int64{tag.ID}, Language: "en", AddedAt: now, UpdatedAt: now,
		Metadata: model.SeriesMetadata{AltTitles: []string{"ブルーピリオド"}, Description: "Art school.", Authors: []string{"Tsubasa Yamaguchi"},
			Genres: []string{"Drama"}, Tags: []string{"Art"}, Publisher: "Kodansha", TotalChapters: 60}}
	other := &model.Series{Title: "Another", SortTitle: "another", Status: model.StatusCompleted, RootFolderID: e.RFID, Path: "Another",
		ProfileID: 1, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
	for _, s := range []*model.Series{ser, other} {
		if _, err := e.App.DB.NewInsert().Model(s).Exec(e.Ctx); err != nil {
			t.Fatal(err)
		}
	}
	f := &komgaFixture{e: e, ser: ser, other: other, tag: tag}
	for i := 1; i <= 4; i++ {
		c := &model.Chapter{SeriesID: ser.ID, NumberKey: strconv.Itoa(i), NumberSort: float64(i), Title: "Chapter " + strconv.Itoa(i),
			Monitored: true, State: model.ChapterMissing, FirstSeenAt: now.Add(time.Duration(i) * time.Minute), UpdatedAt: now}
		if i <= 3 {
			c.State = model.ChapterImported
		}
		if _, err := e.App.DB.NewInsert().Model(c).Exec(e.Ctx); err != nil {
			t.Fatal(err)
		}
		f.chs = append(f.chs, c)
		if i <= 3 {
			cf := &model.ChapterFile{ChapterID: c.ID, SeriesID: ser.ID, RelativePath: "Blue Period Ch." + strconv.Itoa(i) + ".cbz",
				Size: 1 << 20, PageCount: 20, Format: "cbz", Scanlator: "Team", SourceName: "Fake", ImportedAt: now}
			if _, err := e.App.DB.NewInsert().Model(cf).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			c.FileID = &cf.ID
			if _, err := e.App.DB.NewUpdate().Model(c).Column("file_id").WherePK().Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	c := &model.Chapter{SeriesID: other.ID, NumberKey: "1", NumberSort: 1, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
	if _, err := e.App.DB.NewInsert().Model(c).Exec(e.Ctx); err != nil {
		t.Fatal(err)
	}
	rid, err := e.App.Reading.ReaderID(e.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.reader = rid
	read := now.Add(-time.Hour)
	for _, st := range []*model.ChapterReadState{
		{ReaderID: rid, ChapterID: f.chs[0].ID, SeriesID: ser.ID, Completed: true, ReadAt: &read, SyncedAt: now},
		{ReaderID: rid, ChapterID: f.chs[1].ID, SeriesID: ser.ID, Page: 5, SyncedAt: now},
	} {
		if err := readstate.Save(e.Ctx, e.App.DB, st); err != nil {
			t.Fatal(err)
		}
	}
	f.key, _, err = e.App.Komga.CreateKey(e.Ctx, 0, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	f.srv = httptest.NewServer(e.App.Komga.Handler())
	t.Cleanup(f.srv.Close)
	return f
}

func (f *komgaFixture) call(t *testing.T, method, path, body string, out any) {
	t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("X-API-Key", f.key)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s %s: %d", method, path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
}

func (f *komgaFixture) get(t *testing.T, path string, out any) {
	t.Helper()
	f.call(t, "GET", path, "", out)
}

// requireKeys checks dotted paths exist; a trailing "?" allows null.
func requireKeys(t *testing.T, what string, obj map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		nullable := strings.HasSuffix(k, "?")
		k = strings.TrimSuffix(k, "?")
		var cur any = obj
		parts := strings.Split(k, ".")
		for i, p := range parts {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("%s: %s is not an object", what, strings.Join(parts[:i], "."))
			}
			v, present := m[p]
			if !present {
				t.Fatalf("%s: missing %s", what, k)
			}
			cur = v
		}
		if cur == nil && !nullable {
			t.Fatalf("%s: %s is null", what, k)
		}
	}
}

var (
	pageKeys = []string{"content", "empty", "first", "last", "number", "numberOfElements", "size", "totalElements", "totalPages", "sort",
		"pageable.sort.sorted", "pageable.sort.unsorted", "pageable.sort.empty", "pageable.offset", "pageable.pageNumber", "pageable.pageSize",
		"pageable.paged", "pageable.unpaged"}
	// Mihon's Komga extension (SeriesDto), the tracker (the read counts)
	// and KMReader (Series)
	seriesKeys = []string{"id", "libraryId", "name", "url", "created", "lastModified", "fileLastModified", "booksCount", "booksReadCount",
		"booksUnreadCount", "booksInProgressCount", "deleted", "oneshot",
		"metadata.status", "metadata.statusLock", "metadata.title", "metadata.titleLock", "metadata.titleSort", "metadata.titleSortLock",
		"metadata.summary", "metadata.summaryLock", "metadata.readingDirection", "metadata.readingDirectionLock", "metadata.publisher",
		"metadata.publisherLock", "metadata.ageRating?", "metadata.ageRatingLock", "metadata.language", "metadata.languageLock",
		"metadata.genres", "metadata.genresLock", "metadata.tags", "metadata.tagsLock", "metadata.totalBookCount?", "metadata.totalBookCountLock",
		"metadata.sharingLabels", "metadata.links", "metadata.alternateTitles", "metadata.created", "metadata.lastModified",
		"booksMetadata.authors", "booksMetadata.tags", "booksMetadata.releaseDate?", "booksMetadata.summary", "booksMetadata.summaryNumber",
		"booksMetadata.created", "booksMetadata.lastModified"}
	// Mihon's Komga extension (BookDto) and KMReader (Book)
	bookKeys = []string{"id", "seriesId", "seriesTitle", "libraryId", "name", "url", "number", "created", "lastModified", "fileLastModified",
		"sizeBytes", "size", "deleted", "fileHash", "oneshot", "readProgress?",
		"media.status", "media.mediaType", "media.mediaProfile", "media.pagesCount", "media.comment",
		"metadata.title", "metadata.titleLock", "metadata.summary", "metadata.summaryLock", "metadata.number", "metadata.numberLock",
		"metadata.numberSort", "metadata.numberSortLock", "metadata.releaseDate?", "metadata.releaseDateLock", "metadata.authors",
		"metadata.authorsLock", "metadata.tags", "metadata.tagsLock", "metadata.isbn", "metadata.isbnLock", "metadata.links",
		"metadata.linksLock", "metadata.created", "metadata.lastModified"}
	progressKeys = []string{"page", "completed", "readDate", "created", "lastModified"}
)

type page struct {
	Content       []map[string]any `json:"content"`
	TotalElements int              `json:"totalElements"`
	Last          bool             `json:"last"`
}

func ids(p page) []string {
	out := []string{}
	for _, c := range p.Content {
		out = append(out, c["id"].(string))
	}
	return out
}

func sid(id int64) string { return strconv.FormatInt(id, 10) }

// TestKomgaAPICatalog checks what the Mihon extension, its tracker,
// KMReader and Paperback read: every series and every chapter (downloaded
// or not), in the payload shapes each client decodes.
func TestKomgaAPICatalog(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			f := newKomgaFixture(t, dsn)

			// the extension's series list
			var raw map[string]any
			f.get(t, "/api/v1/series?page=0&size=20&sort=metadata.titleSort,asc", &raw)
			requireKeys(t, "series page", raw, pageKeys...)
			var pg page
			f.get(t, "/api/v1/series?page=0&size=20&sort=metadata.titleSort,asc", &pg)
			if got := ids(pg); len(got) != 2 || got[0] != sid(f.other.ID) || got[1] != sid(f.ser.ID) {
				t.Fatalf("series order %v", got)
			}
			for _, s := range pg.Content {
				requireKeys(t, "series", s, seriesKeys...)
			}
			blue := pg.Content[1]
			if blue["booksCount"] != 4.0 || blue["booksReadCount"] != 1.0 || blue["booksInProgressCount"] != 1.0 || blue["booksUnreadCount"] != 2.0 {
				t.Fatalf("counts %v", blue)
			}
			md := blue["metadata"].(map[string]any)
			if md["status"] != "ONGOING" || md["totalBookCount"] != 60.0 || md["publisher"] != "Kodansha" {
				t.Fatalf("metadata %v", md)
			}
			if !strings.HasSuffix(blue["url"].(string), "Blue Period") {
				t.Fatalf("url %v", blue["url"])
			}

			// searches and filters
			for q, want := range map[string]int{
				"search=blue":        1,
				"search=ブルー":         1,
				"search=nothing":     0,
				"status=ENDED":       1,
				"read_status=UNREAD": 1,
				"read_status=IN_PROGRESS&read_status=READ": 1,
				"genre=drama":                    1,
				"collection_id=" + sid(f.tag.ID): 1,
				"library_id=" + sid(f.e.RFID):    2,
				"library_id=999":                 0,
			} {
				var p page
				f.get(t, "/api/v1/series?"+q, &p)
				if p.TotalElements != want {
					t.Fatalf("%s: %d series, want %d", q, p.TotalElements, want)
				}
			}
			var paged page
			f.get(t, "/api/v1/series?page=1&size=1", &paged)
			if len(paged.Content) != 1 || !paged.Last || paged.TotalElements != 2 {
				t.Fatalf("paging %+v", paged)
			}

			// the tracker's series (by the absolute url the extension stores)
			var one map[string]any
			f.get(t, "/api/v1/series/"+sid(f.ser.ID), &one)
			requireKeys(t, "series by id", one, seriesKeys...)

			// every chapter, downloaded or not, sorted by numberSort
			f.get(t, "/api/v1/series/"+sid(f.ser.ID)+"/books?unpaged=true&media_status=READY&deleted=false", &pg)
			if len(pg.Content) != 4 {
				t.Fatalf("books %d", len(pg.Content))
			}
			for i, b := range pg.Content {
				requireKeys(t, "book", b, bookKeys...)
				m := b["metadata"].(map[string]any)
				if m["numberSort"] != float64(i+1) || b["media"].(map[string]any)["status"] != "READY" {
					t.Fatalf("book %d: %v", i, b)
				}
				if rd, ok := m["releaseDate"].(string); !ok || len(rd) != 10 {
					t.Fatalf("releaseDate must be yyyy-MM-dd: %v", m["releaseDate"])
				}
			}
			if !strings.HasSuffix(pg.Content[2]["url"].(string), ".cbz") || strings.HasSuffix(pg.Content[3]["url"].(string), ".cbz") {
				t.Fatalf("only downloaded books end in .cbz: %v / %v", pg.Content[2]["url"], pg.Content[3]["url"])
			}
			if pg.Content[3]["size"] != "not downloaded" || pg.Content[2]["media"].(map[string]any)["pagesCount"] != 20.0 {
				t.Fatalf("sizes %v %v", pg.Content[3]["size"], pg.Content[2]["media"])
			}
			requireKeys(t, "read progress", pg.Content[0]["readProgress"].(map[string]any), progressKeys...)
			if rp := pg.Content[1]["readProgress"].(map[string]any); rp["page"] != 5.0 || rp["completed"] != false {
				t.Fatalf("in progress %v", rp)
			}
			if pg.Content[2]["readProgress"] != nil {
				t.Fatalf("unread has no progress: %v", pg.Content[2]["readProgress"])
			}

			// KMReader: POST list with a condition tree
			var kp page
			f.call(t, "POST", "/api/v1/books/list?page=0&size=50&sort=metadata.numberSort,desc",
				`{"condition":{"allOf":[{"seriesId":{"operator":"is","value":"`+sid(f.ser.ID)+`"}},{"anyOf":[{"readStatus":{"operator":"is","value":"UNREAD"}},{"readStatus":{"operator":"is","value":"IN_PROGRESS"}}]},{"deleted":{"operator":"isFalse"}}]}}`, &kp)
			if got := ids(kp); len(got) != 3 || got[0] != sid(f.chs[3].ID) || got[2] != sid(f.chs[1].ID) {
				t.Fatalf("books/list %v", got)
			}
			// Paperback 0.9: sibling keys are ANDed
			f.call(t, "POST", "/api/v1/series/list?page=0&size=20",
				`{"condition":{"libraryId":{"operator":"is","value":"`+sid(f.e.RFID)+`"},"seriesStatus":{"operator":"isNot","value":"ENDED"}}}`, &kp)
			if got := ids(kp); len(got) != 1 || got[0] != sid(f.ser.ID) {
				t.Fatalf("series/list siblings %v", got)
			}
			f.call(t, "POST", "/api/v1/series/list", `{"fullTextSearch":"another"}`, &kp)
			if got := ids(kp); len(got) != 1 || got[0] != sid(f.other.ID) {
				t.Fatalf("fullTextSearch %v", got)
			}
			f.call(t, "POST", "/api/v1/series/list?sort=lastModified,desc&size=1000", `{"condition":{"allOf":[]}}`, &kp)
			if kp.TotalElements != 2 {
				t.Fatalf("incremental sync %d", kp.TotalElements)
			}

			// next / previous (404 at the ends)
			var b map[string]any
			f.get(t, "/api/v1/books/"+sid(f.chs[1].ID)+"/next", &b)
			if b["id"] != sid(f.chs[2].ID) {
				t.Fatalf("next %v", b["id"])
			}
			f.get(t, "/api/v1/books/"+sid(f.chs[1].ID)+"/previous", &b)
			if b["id"] != sid(f.chs[0].ID) {
				t.Fatalf("previous %v", b["id"])
			}
			req, _ := http.NewRequest("GET", f.srv.URL+"/api/v1/books/"+sid(f.chs[3].ID)+"/next", nil)
			req.Header.Set("X-API-Key", f.key)
			if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 404 {
				t.Fatalf("next at the end: %d", resp.StatusCode)
			}

			// on deck and the "Continue reading" read list: chapter 2 (the
			// first unfinished one after the last read)
			f.get(t, "/api/v1/books/ondeck", &pg)
			if got := ids(pg); len(got) != 1 || got[0] != sid(f.chs[1].ID) {
				t.Fatalf("ondeck %v", got)
			}
			f.get(t, "/api/v1/readlists", &pg)
			if len(pg.Content) != 1 || pg.Content[0]["name"] != "Continue reading" {
				t.Fatalf("readlists %v", pg.Content)
			}
			f.get(t, "/api/v1/readlists/"+pg.Content[0]["id"].(string)+"/books", &pg)
			if got := ids(pg); len(got) != 1 || got[0] != sid(f.chs[1].ID) {
				t.Fatalf("readlist books %v", got)
			}
			// Paperback 0.8's "continue reading"
			f.get(t, "/api/v1/books?sort=readProgress.readDate,desc&read_status=IN_PROGRESS", &pg)
			if got := ids(pg); len(got) != 1 || got[0] != sid(f.chs[1].ID) {
				t.Fatalf("books in progress %v", got)
			}

			// catalog
			var list []string
			f.get(t, "/api/v1/genres", &list)
			if len(list) != 1 || list[0] != "Drama" {
				t.Fatalf("genres %v", list)
			}
			f.get(t, "/api/v1/tags/series", &list)
			if len(list) != 1 || list[0] != "Art" {
				t.Fatalf("tags %v", list)
			}
			f.get(t, "/api/v1/publishers", &list)
			if len(list) != 1 || list[0] != "Kodansha" {
				t.Fatalf("publishers %v", list)
			}
			var authors []map[string]any
			f.get(t, "/api/v1/authors", &authors)
			if len(authors) != 1 || authors[0]["name"] != "Tsubasa Yamaguchi" || authors[0]["role"] != "writer" {
				t.Fatalf("authors %v", authors)
			}
			f.get(t, "/api/v2/authors", &raw)
			requireKeys(t, "authors page", raw, pageKeys...)
			f.get(t, "/api/v1/collections", &pg)
			if len(pg.Content) != 1 || pg.Content[0]["name"] != "favourites" {
				t.Fatalf("collections %v", pg.Content)
			}
			f.get(t, "/api/v1/collections/"+sid(f.tag.ID)+"/series", &pg)
			if got := ids(pg); len(got) != 1 || got[0] != sid(f.ser.ID) {
				t.Fatalf("collection series %v", got)
			}
			f.get(t, "/api/v1/history", &raw)
			requireKeys(t, "history", raw, pageKeys...)
		})
	}
}
