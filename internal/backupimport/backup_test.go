package backupimport

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	"howett.net/plist"
)

// pb builds protobuf messages for fixtures.
type pb struct{ b []byte }

func (p *pb) key(num int, typ wireType) { p.b = binary.AppendUvarint(p.b, uint64(num)<<3|uint64(typ)) }
func (p *pb) varint(num int, v int64) *pb {
	p.key(num, wireVarint)
	p.b = binary.AppendUvarint(p.b, uint64(v))
	return p
}
func (p *pb) bytes(num int, v []byte) *pb {
	p.key(num, wireBytes)
	p.b = binary.AppendUvarint(p.b, uint64(len(v)))
	p.b = append(p.b, v...)
	return p
}
func (p *pb) str(num int, s string) *pb { return p.bytes(num, []byte(s)) }
func (p *pb) msg(num int, m *pb) *pb    { return p.bytes(num, m.b) }
func (p *pb) float(num int, f float32) *pb {
	p.key(num, wire32)
	p.b = binary.LittleEndian.AppendUint32(p.b, math.Float32bits(f))
	return p
}

func mihonFixture() []byte {
	ch1 := (&pb{}).str(1, "/chapter/1").str(2, "Chapter 1").str(3, "Group A").varint(4, 1).float(9, 1)
	ch2 := (&pb{}).str(1, "/chapter/2").str(2, "Chapter 2").varint(6, 11).float(9, 2)
	ch0 := (&pb{}).str(1, "/chapter/0").str(2, "Prologue") // number 0 is omitted
	anilist := (&pb{}).varint(1, 2).varint(2, 9).varint(100, 30013)
	malOld := (&pb{}).varint(1, 1).varint(3, 13) // old mediaIdInt only
	hist := (&pb{}).str(1, "/chapter/1").varint(2, 1700000000000)
	packed := binary.AppendUvarint(nil, 2)
	packed = binary.AppendUvarint(packed, 5)
	m1 := (&pb{}).varint(1, 2499283573021220255).str(2, "/manga/abc").str(3, "One Piece").str(5, "Oda").
		str(7, "Action").str(7, "Adventure").varint(8, 1).str(9, "https://x/cover.jpg").varint(13, 1600000000000).
		msg(16, ch1).msg(16, ch2).msg(16, ch0).bytes(17, packed).msg(18, anilist).msg(18, malOld).msg(104, hist).
		str(108, "Bad Scans").
		str(9001, "fork field").varint(800, 3) // fork fields are skipped
	m2 := (&pb{}).varint(1, 1234).str(2, "/series/2").str(3, "Not favorite").varint(8, 6).varint(100, 0).varint(17, 2)
	cat1 := (&pb{}).str(1, "Reading").varint(2, 2)
	cat2 := (&pb{}).str(1, "Plan").varint(2, 5)
	src := (&pb{}).str(1, "MangaDex").varint(2, 2499283573021220255)
	return (&pb{}).msg(1, m1).msg(1, m2).msg(2, cat2).msg(2, cat1).msg(101, src).b
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func TestMihon(t *testing.T) {
	for name, data := range map[string][]byte{"gzip": gz(mihonFixture()), "raw": mihonFixture()} {
		t.Run(name, func(t *testing.T) {
			b, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if b.Format != FormatMihon || len(b.Entries) != 2 {
				t.Fatalf("got %s with %d entries", b.Format, len(b.Entries))
			}
			if got := b.Categories; len(got) != 2 || got[0] != "Reading" || got[1] != "Plan" {
				t.Fatalf("categories = %v", got)
			}
			e := b.Entries[0]
			if e.SourceID != "2499283573021220255" || e.SourceName != "MangaDex" || e.URL != "/manga/abc" || e.Title != "One Piece" {
				t.Fatalf("entry = %+v", e)
			}
			if e.Status != "ongoing" || e.Author != "Oda" || len(e.Genres) != 2 || !e.Favorite || e.AddedAt.Year() != 2020 {
				t.Fatalf("entry = %+v", e)
			}
			if e.Trackers[TrackerAniList] != "30013" || e.Trackers[TrackerMAL] != "13" {
				t.Fatalf("trackers = %v", e.Trackers)
			}
			if len(e.Categories) != 2 || e.Categories[0] != "Reading" || e.Categories[1] != "Plan" {
				t.Fatalf("categories = %v", e.Categories)
			}
			if len(e.ExcludedScanlators) != 1 || e.ExcludedScanlators[0] != "Bad Scans" {
				t.Fatalf("excluded = %v", e.ExcludedScanlators)
			}
			if len(e.Chapters) != 3 {
				t.Fatalf("chapters = %+v", e.Chapters)
			}
			c1, c2, c0 := e.Chapters[0], e.Chapters[1], e.Chapters[2]
			if !c1.Read || c1.Number != 1 || c1.Scanlator != "Group A" || c1.ReadAt == nil || c1.ReadAt.Unix() != 1700000000 {
				t.Fatalf("chapter 1 = %+v", c1)
			}
			if c2.Read || c2.LastPageRead != 11 || c2.Number != 2 || c2.ReadAt != nil {
				t.Fatalf("chapter 2 = %+v", c2)
			}
			if c0.Number != 0 {
				t.Fatalf("chapter 0 = %+v", c0)
			}
			if e.ReadCount() != 1 {
				t.Fatal("read count")
			}
			n := b.Entries[1]
			if n.Favorite || n.Status != "hiatus" || len(n.Categories) != 1 || n.Categories[0] != "Reading" {
				t.Fatalf("second entry = %+v", n)
			}
		})
	}
}

func TestMihonTruncated(t *testing.T) {
	data := mihonFixture()
	if _, err := Parse(data[:len(data)-7]); err == nil {
		t.Fatal("truncated backup parsed")
	}
}

func TestRejectsLegacyJSON(t *testing.T) {
	_, err := Parse([]byte(`{"version":2,"mangas":[{"manga":["/manga/1","x"]}],"categories":[]}`))
	if !errors.Is(err, ErrLegacyJSON) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Parse([]byte("PK\x03\x04zip")); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("err = %v", err)
	}
}

func aidokuTree(date any, newShapes bool) map[string]any {
	var cats, sources []any
	if newShapes {
		cats = []any{map[string]any{"title": "Reading", "sort": 0}}
		sources = []any{map[string]any{"id": "multi.mangadex", "apiVersion": "0.7"}}
	} else {
		cats = []any{"Reading"}
		sources = []any{"multi.mangadex"}
	}
	return map[string]any{
		"date": date,
		"library": []any{
			map[string]any{"mangaId": "a96676e5-8ae2-425e-b549-7f15dd34a6d8", "sourceId": "multi.mangadex", "categories": []any{"Reading"},
				"dateAdded": date, "lastOpened": date, "lastUpdated": date},
			map[string]any{"mangaId": "/series/01J76XY/solo", "sourceId": "en.weebcentral", "dateAdded": date, "lastOpened": date, "lastUpdated": date},
		},
		"manga": []any{
			map[string]any{"id": "a96676e5-8ae2-425e-b549-7f15dd34a6d8", "sourceId": "multi.mangadex", "title": "Komi Can't Communicate",
				"author": "Oda Tomohito", "status": 2, "nsfw": 0, "viewer": 0, "tags": []any{"Comedy"}, "scanlatorFilter": []any{"Bad"},
				"url": "https://mangadex.org/title/a96676e5-8ae2-425e-b549-7f15dd34a6d8"},
			map[string]any{"id": "/series/01J76XY/solo", "sourceId": "en.weebcentral", "title": "Solo Leveling", "status": 1, "nsfw": 0, "viewer": 0},
		},
		"chapters": []any{
			map[string]any{"sourceId": "multi.mangadex", "mangaId": "a96676e5-8ae2-425e-b549-7f15dd34a6d8", "id": "c1", "lang": "en", "chapter": 1.0, "sourceOrder": 1},
			map[string]any{"sourceId": "multi.mangadex", "mangaId": "a96676e5-8ae2-425e-b549-7f15dd34a6d8", "id": "c2", "lang": "en", "chapter": 2.5, "sourceOrder": 0},
		},
		"history": []any{
			map[string]any{"sourceId": "multi.mangadex", "mangaId": "a96676e5-8ae2-425e-b549-7f15dd34a6d8", "chapterId": "c1", "completed": true, "dateRead": date},
			map[string]any{"sourceId": "multi.mangadex", "mangaId": "a96676e5-8ae2-425e-b549-7f15dd34a6d8", "chapterId": "c2", "completed": false, "progress": 4, "total": 20, "dateRead": date},
		},
		"trackItems": []any{
			map[string]any{"id": "97852", "trackerId": "anilist", "mangaId": "a96676e5-8ae2-425e-b549-7f15dd34a6d8", "sourceId": "multi.mangadex"},
			map[string]any{"id": "x", "trackerId": "bangumi", "mangaId": "a96676e5-8ae2-425e-b549-7f15dd34a6d8", "sourceId": "multi.mangadex"},
		},
		"categories": cats,
		"sources":    sources,
		"version":    "0.7.0",
	}
}

func checkAidoku(t *testing.T, b *Backup) {
	t.Helper()
	if b.Format != FormatAidoku || len(b.Entries) != 2 {
		t.Fatalf("got %s with %d entries", b.Format, len(b.Entries))
	}
	if len(b.Categories) != 1 || b.Categories[0] != "Reading" || b.Sources["multi.mangadex"] != "mangadex" {
		t.Fatalf("categories %v sources %v", b.Categories, b.Sources)
	}
	e := b.Entries[0]
	if e.SourceID != "multi.mangadex" || e.URL != "a96676e5-8ae2-425e-b549-7f15dd34a6d8" || e.Title != "Komi Can't Communicate" || e.Status != "completed" {
		t.Fatalf("entry = %+v", e)
	}
	if e.Trackers[TrackerAniList] != "97852" || len(e.Trackers) != 1 || e.WebURL == "" || len(e.ExcludedScanlators) != 1 {
		t.Fatalf("entry = %+v", e)
	}
	if len(e.Chapters) != 2 || e.Chapters[0].Number != 2.5 || e.Chapters[0].LastPageRead != 3 || e.Chapters[0].Read {
		t.Fatalf("chapters = %+v", e.Chapters)
	}
	if !e.Chapters[1].Read || e.Chapters[1].ReadAt == nil || e.Chapters[1].ReadAt.Year() != 2024 || e.Chapters[1].Lang != "en" {
		t.Fatalf("chapters = %+v", e.Chapters)
	}
	if b.Entries[1].SourceName != "weebcentral" || b.Entries[1].URL != "/series/01J76XY/solo" {
		t.Fatalf("entry 2 = %+v", b.Entries[1])
	}
}

func TestAidokuPlist(t *testing.T) {
	date := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	for _, shapes := range []bool{false, true} {
		for _, format := range []int{plist.BinaryFormat, plist.XMLFormat} {
			data, err := plist.Marshal(aidokuTree(date, shapes), format)
			if err != nil {
				t.Fatal(err)
			}
			b, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			checkAidoku(t, b)
		}
	}
}

func TestAidokuJSON(t *testing.T) {
	data := []byte(`{"date": 1714564800, "version": "0.6", ` +
		`"library":[{"mangaId":"a96676e5-8ae2-425e-b549-7f15dd34a6d8","sourceId":"multi.mangadex","categories":["Reading"],"dateAdded":1714564800},` +
		`{"mangaId":"/series/01J76XY/solo","sourceId":"en.weebcentral","dateAdded":1714564800}],` +
		`"manga":[{"id":"a96676e5-8ae2-425e-b549-7f15dd34a6d8","sourceId":"multi.mangadex","title":"Komi Can't Communicate","status":2,"scanlatorFilter":["Bad"],"url":"https://mangadex.org/title/a96676e5"}],` +
		`"chapters":[{"sourceId":"multi.mangadex","mangaId":"a96676e5-8ae2-425e-b549-7f15dd34a6d8","id":"c1","lang":"en","chapter":1},` +
		`{"sourceId":"multi.mangadex","mangaId":"a96676e5-8ae2-425e-b549-7f15dd34a6d8","id":"c2","lang":"en","chapter":2.5}],` +
		`"history":[{"sourceId":"multi.mangadex","mangaId":"a96676e5-8ae2-425e-b549-7f15dd34a6d8","chapterId":"c1","completed":true,"dateRead":1714564800},` +
		`{"sourceId":"multi.mangadex","mangaId":"a96676e5-8ae2-425e-b549-7f15dd34a6d8","chapterId":"c2","completed":false,"progress":4,"dateRead":1714564800}],` +
		`"trackItems":[{"id":"97852","trackerId":"anilist","mangaId":"a96676e5-8ae2-425e-b549-7f15dd34a6d8","sourceId":"multi.mangadex"}],` +
		`"categories":["Reading"],"sources":["multi.mangadex"]}`)
	b, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	checkAidoku(t, b)
	if b.Entries[1].Title != "/series/01J76XY/solo" {
		t.Fatalf("entry without manga row = %+v", b.Entries[1])
	}
}

func TestResumeFrom(t *testing.T) {
	e := BackupManga{Chapters: []BackupChapter{{Number: 1, Read: true}, {Number: 2, Read: true}, {Number: 2.5}, {Number: 3}, {Number: 0.5}}}
	if from, ok := e.ResumeFrom(); !ok || from < 2.4 || from > 2.5 {
		t.Fatalf("from = %v %v", from, ok)
	}
	e = BackupManga{Chapters: []BackupChapter{{Number: 1, Read: true}, {Number: 2, Read: true}}}
	if from, ok := e.ResumeFrom(); !ok || from <= 2 || from > 2.01 {
		t.Fatalf("from = %v %v", from, ok)
	}
	if _, ok := (BackupManga{Chapters: []BackupChapter{{Number: 1}}}).ResumeFrom(); ok {
		t.Fatal("nothing read")
	}
}

func TestMarshalMihonRoundTrip(t *testing.T) {
	in, err := Parse(mihonFixture())
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse(MarshalMihon(in))
	if err != nil {
		t.Fatal(err)
	}
	a, b := in.Entries[0], out.Entries[0]
	if a.Title != b.Title || a.SourceID != b.SourceID || len(a.Chapters) != len(b.Chapters) || b.Trackers[TrackerAniList] != "30013" ||
		!b.Chapters[0].Read || b.Chapters[0].ReadAt == nil || b.Chapters[1].LastPageRead != 11 || len(b.Categories) != 2 ||
		out.Entries[1].Favorite || len(b.ExcludedScanlators) != 1 || out.Sources[a.SourceID] != "MangaDex" {
		t.Fatalf("round trip lost data:\n%+v\n%+v", a, b)
	}
}

func TestMarshalMihonSourcePreferences(t *testing.T) {
	in, err := Parse(mihonFixture())
	if err != nil {
		t.Fatal(err)
	}
	data := MarshalMihon(in, MihonSourcePreferences{SourceID: "4508733312114627536", Strings: map[string]string{
		"Address": "https://manga.example.com", "API key": "mangarr_test_key",
	}})
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	if err := fields(raw, func(f field) error {
		if f.num != 105 {
			return nil
		}
		var source string
		var prefs [][]byte
		if err := fields(f.bytes, func(g field) error {
			if g.num == 1 {
				source = g.str()
			} else if g.num == 2 {
				prefs = append(prefs, g.bytes)
			}
			return nil
		}); err != nil {
			return err
		}
		if source != "source_4508733312114627536" {
			return nil
		}
		for _, rawPref := range prefs {
			var key string
			var wrapped []byte
			if err := fields(rawPref, func(g field) error {
				if g.num == 1 {
					key = g.str()
				} else if g.num == 2 {
					wrapped = g.bytes
				}
				return nil
			}); err != nil {
				return err
			}
			var className string
			var valueMessage []byte
			if err := fields(wrapped, func(g field) error {
				if g.num == 1 {
					className = g.str()
				} else if g.num == 2 {
					valueMessage = g.bytes
				}
				return nil
			}); err != nil {
				return err
			}
			if className != "eu.kanade.tachiyomi.data.backup.models.StringPreferenceValue" {
				t.Fatalf("preference class = %q", className)
			}
			if err := fields(valueMessage, func(g field) error {
				if g.num == 1 {
					got[key] = g.str()
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got["Address"] != "https://manga.example.com" || got["API key"] != "mangarr_test_key" {
		t.Fatalf("source preferences = %v", got)
	}
}

// TestReadUpTo: a position counts the chapters below it as read and resumes
// at the chapter reached.
func TestReadUpTo(t *testing.T) {
	e := BackupManga{ReadUpTo: &ReadUpTo{Chapter: 12, Page: 3}}
	if n := e.ReadCount(); n != 11 {
		t.Errorf("read count = %d", n)
	}
	if !e.ReadsChapter(11.5) || e.ReadsChapter(12) || e.ReadsChapter(-1) {
		t.Error("reads chapter")
	}
	if from, ok := e.ResumeFrom(); !ok || from > 12 || from < 11.99 {
		t.Errorf("resume = %v %v", from, ok)
	}
	if _, ok := (BackupManga{ReadUpTo: &ReadUpTo{Chapter: 1}}).ResumeFrom(); ok {
		t.Error("nothing read yet")
	}
}
