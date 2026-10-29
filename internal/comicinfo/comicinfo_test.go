package comicinfo

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() Input {
	d := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)
	return Input{
		SeriesTitle: "One Piece", ChapterNumberKey: "1044.5", Volume: "104", ChapterTitle: "Warrior of Liberation",
		Summary: "Pirates & <treasure>", Authors: []string{"Oda Eiichiro", "oda eiichiro"}, Artists: []string{"Oda Eiichiro"},
		Scanlator: "TCB Scans", Genres: []string{"Action", "Adventure"}, Tags: []string{"Pirates"},
		WebLinks: []string{"https://mangadex.org/title/x", "https://anilist.co/manga/30013"}, PageCount: 17,
		Language: "en", ReadingDirection: "rtl", AgeRating: "Teen", Score: 8.47, ReleaseDate: &d,
	}
}

func TestBuild(t *testing.T) {
	ci := Build(sample())
	if ci.Number != "1044.5" || ci.Volume != 104 || ci.Manga != "YesAndRightToLeft" || ci.Writer != "Oda Eiichiro" {
		t.Fatalf("unexpected %+v", ci)
	}
	if ci.CommunityRating != "4.2" {
		t.Errorf("community rating %q", ci.CommunityRating)
	}
	if ci.Year != 2024 || ci.Month != 3 || ci.Day != 5 {
		t.Fatalf("date %+v", ci)
	}
	b, err := ci.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "&lt;treasure&gt;") || !strings.HasPrefix(s, "<?xml") {
		t.Fatalf("bad xml:\n%s", s)
	}
	// Element order must follow the XSD sequence.
	if strings.Index(s, "<Translator>") > strings.Index(s, "<Publisher>") && strings.Contains(s, "<Publisher>") {
		t.Fatal("order")
	}
}

// TestXSD validates against the ComicInfo v2.1 schema when xmllint exists.
func TestXSD(t *testing.T) {
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Skip("xmllint not installed")
	}
	for name, in := range map[string]Input{"full": sample(), "minimal": {SeriesTitle: "X", ChapterNumberKey: "1"}, "webtoon": {SeriesTitle: "Y", ChapterNumberKey: "2", ReadingDirection: "webtoon", Special: true}} {
		b, err := Build(in).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), name+".xml")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("xmllint", "--noout", "--schema", "testdata/ComicInfo-v2.1.xsd", p).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: schema validation failed: %v\n%s\n%s", name, err, out, b)
		}
	}
}

func TestSeriesJSONHasRequiredFields(t *testing.T) {
	b, err := BuildSeriesJSON(SeriesInput{Title: "One Piece", ID: "anilist:30013", Year: 1997, Description: "d", CoverURL: "c", AgeRating: "Teen"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	md := doc.Metadata
	for _, k := range []string{"type", "publisher", "name", "comicid", "year", "booktype", "comic_image", "total_issues", "publication_run", "status"} {
		if _, ok := md[k]; !ok {
			t.Errorf("missing required key %s", k)
		}
	}
	if md["status"] != "Continuing" || md["age_rating"] != "12+" || md["volume"] != nil {
		t.Errorf("unexpected: %v", md)
	}
}
