package app_test

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
)

func TestKOReaderFeedsAndSync(t *testing.T) {
	for _, dsn := range dbtest.DSNs(t) {
		t.Run(dsn, func(t *testing.T) {
			e := newTestApp(t, dsn)
			now := time.Now().UTC()
			tag := &model.Tag{Label: "visible"}
			if _, err := e.App.DB.NewInsert().Model(tag).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			visible := &model.Series{Title: "Visible Placeholder", SortTitle: "visible placeholder", Status: model.StatusOngoing, MonitorNew: model.MonitorAll, RootFolderID: e.RFID, Path: "visible", ProfileID: 1, Tags: []int64{tag.ID}, Language: "en", AddedAt: now, UpdatedAt: now}
			hidden := &model.Series{Title: "Hidden Placeholder", SortTitle: "hidden placeholder", Status: model.StatusOngoing, MonitorNew: model.MonitorAll, RootFolderID: e.RFID, Path: "hidden", ProfileID: 1, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
			for _, s := range []*model.Series{visible, hidden} {
				if _, err := e.App.DB.NewInsert().Model(s).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
			}
			for i := 2; i <= 22; i++ {
				title := fmt.Sprintf("Visible library entry %02d", i)
				extra := &model.Series{Title: title, SortTitle: strings.ToLower(title), Status: model.StatusOngoing, MonitorNew: model.MonitorAll, RootFolderID: e.RFID, Path: fmt.Sprintf("visible-%02d", i), ProfileID: 1, Tags: []int64{tag.ID}, Language: "en", AddedAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(extra).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
			}
			hiddenChapter := &model.Chapter{SeriesID: hidden.ID, NumberKey: "1", NumberSort: 1, Title: "Secret chapter", State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(hiddenChapter).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			chapter := &model.Chapter{SeriesID: visible.ID, NumberKey: "1", NumberSort: 1, Title: "Chapter 1", State: model.ChapterImported, FirstSeenAt: now, UpdatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(chapter).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			modernPage, err := os.ReadFile(filepath.Join("..", "imagecheck", "testdata", "color.avif"))
			if err != nil {
				t.Fatal(err)
			}
			seriesDir, err := e.App.Library.SeriesDir(e.Ctx, visible)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(seriesDir, 0o755); err != nil {
				t.Fatal(err)
			}
			cbzPath := filepath.Join(seriesDir, "chapter-1.cbz")
			archive, err := os.Create(cbzPath)
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(archive)
			entry, err := zw.Create("0001.avif")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write(modernPage); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(cbzPath)
			if err != nil {
				t.Fatal(err)
			}
			chapterFile := &model.ChapterFile{ChapterID: chapter.ID, SeriesID: visible.ID, RelativePath: "chapter-1.cbz", Size: info.Size(), PageCount: 1, Format: "cbz", ImportedAt: now}
			if _, err := e.App.DB.NewInsert().Model(chapterFile).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			chapter.FileID = &chapterFile.ID
			if _, err := e.App.DB.NewUpdate().Model(chapter).Column("file_id").WherePK().Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			groups, err := e.App.Auth.EnsureGroups(e.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			group := &model.Group{}
			if err := e.App.DB.NewSelect().Model(group).Where("id = ?", groups[model.GroupUsers]).Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			group.IncludeTags = []int64{tag.ID}
			if _, err := e.App.DB.NewUpdate().Model(group).Column("include_tags").WherePK().Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			user, err := e.App.Auth.CreateUser(e.Ctx, auth.NewUser{Username: "reader", Password: "reader-password-1", GroupID: group.ID})
			if err != nil {
				t.Fatal(err)
			}
			key, _, err := e.App.Komga.CreateKey(e.Ctx, user.ID, "KOReader device", "KOReader")
			if err != nil {
				t.Fatal(err)
			}
			request := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.SetBasicAuth(user.Username, "reader-password-1")
				for k, v := range headers {
					req.Header.Set(k, v)
				}
				rec := httptest.NewRecorder()
				e.App.Komga.Handler().ServeHTTP(rec, req)
				return rec
			}
			feed := request(http.MethodGet, "/opds/libraries", "", nil)
			if feed.Code != 200 {
				t.Fatalf("libraries: %d %s", feed.Code, feed.Body.String())
			}
			var parsed struct {
				XMLName xml.Name `xml:"feed"`
				Entries []struct {
					Title string `xml:"title"`
				} `xml:"entry"`
			}
			if err := xml.Unmarshal(feed.Body.Bytes(), &parsed); err != nil {
				t.Fatalf("invalid Atom: %v", err)
			}
			if len(parsed.Entries) != 1 || parsed.Entries[0].Title != "manga" {
				t.Fatalf("visibility leaked or library missing: %+v", parsed.Entries)
			}
			library := request(http.MethodGet, "/opds/libraries/"+strconv.FormatInt(e.RFID, 10)+"?_count=100", "", nil)
			if library.Code != 200 || !strings.Contains(library.Body.String(), "Visible Placeholder") || strings.Contains(library.Body.String(), "Hidden Placeholder") {
				t.Fatalf("library visibility: %d %s", library.Code, library.Body.String())
			}
			page := request(http.MethodGet, "/opds/libraries/"+strconv.FormatInt(e.RFID, 10)+"?_count=1&_start=1", "", nil)
			var pageFeed struct {
				Entries []struct {
					Title string `xml:"title"`
				} `xml:"entry"`
			}
			if page.Code != 200 || xml.Unmarshal(page.Body.Bytes(), &pageFeed) != nil || len(pageFeed.Entries) != 1 || !strings.Contains(page.Body.String(), `rel="next"`) {
				t.Fatalf("feed pagination: %d %s", page.Code, page.Body.String())
			}
			bad := httptest.NewRecorder()
			e.App.Komga.Handler().ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/opds", nil))
			if bad.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated feed: %d", bad.Code)
			}
			if got := request(http.MethodGet, "/opds/search?q=Visible&_count=100", "", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "Visible Placeholder") {
				t.Fatalf("search: %d %s", got.Code, got.Body.String())
			}
			if got := request(http.MethodGet, "/opds/search?q=Hidden", "", nil); got.Code != 200 || strings.Contains(got.Body.String(), "Hidden Placeholder") {
				t.Fatalf("search visibility: %d %s", got.Code, got.Body.String())
			}
			if got := request(http.MethodGet, "/opds/updated", "", nil); got.Code != 200 || strings.Contains(got.Body.String(), "Secret chapter") {
				t.Fatalf("updated feed visibility: %d %s", got.Code, got.Body.String())
			}
			seriesFeed := request(http.MethodGet, "/opds/series/"+strconv.FormatInt(visible.ID, 10), "", nil)
			if seriesFeed.Code != 200 || !strings.Contains(seriesFeed.Body.String(), "application/vnd.comicbook+zip") {
				t.Fatalf("chapter acquisition: %d %s", seriesFeed.Code, seriesFeed.Body.String())
			}
			// CrossPoint drops entries without an application/epub+zip acquisition link.
			var chapters struct {
				Entries []struct {
					Title string `xml:"title"`
					Links []struct {
						Rel  string `xml:"rel,attr"`
						Href string `xml:"href,attr"`
						Type string `xml:"type,attr"`
					} `xml:"link"`
				} `xml:"entry"`
			}
			if err := xml.Unmarshal(seriesFeed.Body.Bytes(), &chapters); err != nil || len(chapters.Entries) != 1 {
				t.Fatalf("series feed: %v %s", err, seriesFeed.Body.String())
			}
			epubHref := ""
			for _, l := range chapters.Entries[0].Links {
				if strings.Contains(l.Rel, "opds-spec.org/acquisition") && l.Type == "application/epub+zip" {
					epubHref = l.Href
				}
			}
			if epubHref == "" {
				t.Fatalf("chapter has no EPUB acquisition link: %s", seriesFeed.Body.String())
			}
			epub := request(http.MethodGet, epubHref, "", nil)
			if epub.Code != 200 || epub.Header().Get("Content-Type") != "application/epub+zip" {
				t.Fatalf("EPUB acquisition: %d %s", epub.Code, epub.Body.String())
			}
			er, err := zip.NewReader(bytes.NewReader(epub.Body.Bytes()), int64(epub.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			if er.File[0].Name != "mimetype" || er.File[0].Method != zip.Store {
				t.Fatalf("EPUB must start with a stored mimetype: %+v", er.File[0].FileHeader)
			}
			epubFiles := map[string]*zip.File{}
			for _, f := range er.File {
				epubFiles[f.Name] = f
			}
			for _, name := range []string{"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/nav.xhtml", "OEBPS/p0001.xhtml", "OEBPS/images/0001.jpg"} {
				if epubFiles[name] == nil {
					t.Fatalf("EPUB is missing %s: %v", name, epubFiles)
				}
			}
			rc, err := epubFiles["OEBPS/content.opf"].Open()
			if err != nil {
				t.Fatal(err)
			}
			opf, _ := io.ReadAll(rc)
			_ = rc.Close()
			if !strings.Contains(string(opf), `href="images/0001.jpg" media-type="image/jpeg"`) || !strings.Contains(string(opf), `<itemref idref="p1"/>`) {
				t.Fatalf("EPUB package: %s", opf)
			}
			// XTCH: pages pre-rendered for CrossPoint's 480x800 screen, or another size.
			for _, c := range []struct {
				query string
				w, h  uint16
			}{{"", 480, 800}, {"?screen=528x792", 528, 792}} {
				xtch := request(http.MethodGet, "/opds/chapters/"+strconv.FormatInt(chapter.ID, 10)+"/xtch"+c.query, "", nil)
				d := xtch.Body.Bytes()
				if xtch.Code != 200 || len(d) < 0x100 || string(d[:4]) != "XTCH" {
					t.Fatalf("XTCH acquisition %q: %d %.64q", c.query, xtch.Code, d)
				}
				entry := d[binary.LittleEndian.Uint64(d[0x18:]):]
				if binary.LittleEndian.Uint16(d[6:]) != 1 || binary.LittleEndian.Uint16(entry[12:]) != c.w || binary.LittleEndian.Uint16(entry[14:]) != c.h {
					t.Fatalf("XTCH %q page table: % x", c.query, entry[:16])
				}
			}
			if got := request(http.MethodGet, "/opds/chapters/"+strconv.FormatInt(chapter.ID, 10)+"/xtch?screen=huge", "", nil); got.Code != http.StatusBadRequest {
				t.Fatalf("bad screen size: %d", got.Code)
			}
			// CrossPoint's own EPUB download gets screen-sized grayscale pages.
			device := request(http.MethodGet, epubHref, "", map[string]string{"User-Agent": "CrossPoint-ESP32-1.6.5"})
			dr, err := zip.NewReader(bytes.NewReader(device.Body.Bytes()), int64(device.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, f := range dr.File {
				if f.Name != "OEBPS/images/0001.jpg" {
					continue
				}
				found = true
				rc, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				cfg, err := jpeg.DecodeConfig(rc)
				_ = rc.Close()
				if err != nil || cfg.Width != 480 || cfg.Height != 800 || cfg.ColorModel != color.GrayModel {
					t.Fatalf("device EPUB page: %+v %v", cfg, err)
				}
			}
			if !found {
				t.Fatal("device EPUB has no pages")
			}
			download := request(http.MethodGet, "/opds/chapters/"+strconv.FormatInt(chapter.ID, 10), "", nil)
			if download.Code != 200 {
				t.Fatalf("CBZ acquisition: %d %s", download.Code, download.Body.String())
			}
			zr, err := zip.NewReader(bytes.NewReader(download.Body.Bytes()), int64(download.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			if len(zr.File) != 1 || filepath.Ext(zr.File[0].Name) != ".jpg" {
				t.Fatalf("unsupported page was not converted: %+v", zr.File)
			}
			pageReader, err := zr.File[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			converted, err := io.ReadAll(pageReader)
			_ = pageReader.Close()
			if err != nil {
				t.Fatal(err)
			}
			if len(converted) < 3 || converted[0] != 0xff || converted[1] != 0xd8 {
				t.Fatalf("converted page is not JPEG")
			}
			// KOReader's default sampled digest of the downloaded archive resolves to the chapter.
			digest := partialKODigest(download.Body.Bytes())
			passwordHash := md5.Sum([]byte(key))
			authHeaders := map[string]string{"X-Auth-User": user.Username, "X-Auth-Key": hex.EncodeToString(passwordHash[:]), "Accept": "application/vnd.koreader.v1+json"}
			if got := request(http.MethodGet, "/users/auth", "", authHeaders); got.Code != 200 {
				t.Fatalf("kosync auth: %d %s", got.Code, got.Body.String())
			}
			if got := request(http.MethodPost, "/users/create", "{}", nil); got.Code != http.StatusForbidden {
				t.Fatalf("account creation: %d", got.Code)
			}
			body := `{"document":"` + digest + `","progress":"2","percentage":0.5,"device":"test device"}`
			if got := request(http.MethodPut, "/syncs/progress", body, authHeaders); got.Code != 200 {
				t.Fatalf("kosync update: %d %s", got.Code, got.Body.String())
			}
			var state model.ChapterReadState
			if err := e.App.DB.NewSelect().Model(&state).Where("reader_id = ? AND chapter_id = ?", user.ReaderID, chapter.ID).Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			if state.Page != 2 || state.Completed {
				t.Fatalf("unexpected stored progress: %+v", state)
			}
			if got := request(http.MethodGet, "/syncs/progress/"+digest, "", authHeaders); got.Code != 200 || !strings.Contains(got.Body.String(), `"progress":"2"`) {
				t.Fatalf("kosync read: %d %s", got.Code, got.Body.String())
			}
			// API-key hashes are account-bound; another account cannot read the document.
			other, err := e.App.Auth.CreateUser(e.Ctx, auth.NewUser{Username: "other", Password: "other-password-1", GroupID: group.ID})
			if err != nil {
				t.Fatal(err)
			}
			otherKey, _, err := e.App.Komga.CreateKey(e.Ctx, other.ID, "another device", "KOReader")
			if err != nil {
				t.Fatal(err)
			}
			otherHash := md5.Sum([]byte(otherKey))
			otherHeaders := map[string]string{"X-Auth-User": other.Username, "X-Auth-Key": hex.EncodeToString(otherHash[:])}
			if got := request(http.MethodGet, "/syncs/progress/"+digest, "", otherHeaders); got.Code != 200 || strings.Contains(got.Body.String(), `"percentage"`) {
				t.Fatalf("progress leaked: %d %s", got.Code, got.Body.String())
			}
		})
	}
}

func partialKODigest(data []byte) string {
	h := md5.New()
	for i := -1; i <= 10; i++ {
		offset := 0 // LuaJIT: 1024 << -2 shifts by 30 and wraps to 0
		if i >= 0 {
			offset = 1024 << (2 * i)
		}
		if offset >= len(data) {
			break
		}
		end := offset + 1024
		if end > len(data) {
			end = len(data)
		}
		_, _ = h.Write(data[offset:end])
	}
	return hex.EncodeToString(h.Sum(nil))
}
