// Package comicinfo writes ComicInfo.xml (anansi-project v2.1 draft element
// order, compatible with Komga and Kavita) and Mylar-style series.json.
package comicinfo

import (
	"encoding/json"
	"encoding/xml"
	"math"
	"strconv"
	"strings"
	"time"
)

// ComicInfo fields in XSD sequence order. Zero values are omitted.
type ComicInfo struct {
	XMLName     xml.Name `xml:"ComicInfo"`
	XMLNSXsi    string   `xml:"xmlns:xsi,attr,omitempty"`
	XMLNSXsd    string   `xml:"xmlns:xsd,attr,omitempty"`
	Title       string   `xml:"Title,omitempty"`
	Series      string   `xml:"Series,omitempty"`
	Number      string   `xml:"Number,omitempty"`
	Count       int      `xml:"Count,omitempty"`
	Volume      int      `xml:"Volume,omitempty"`
	Summary     string   `xml:"Summary,omitempty"`
	Notes       string   `xml:"Notes,omitempty"`
	Year        int      `xml:"Year,omitempty"`
	Month       int      `xml:"Month,omitempty"`
	Day         int      `xml:"Day,omitempty"`
	Writer      string   `xml:"Writer,omitempty"`
	Penciller   string   `xml:"Penciller,omitempty"`
	Translator  string   `xml:"Translator,omitempty"`
	Publisher   string   `xml:"Publisher,omitempty"`
	Genre       string   `xml:"Genre,omitempty"`
	Tags        string   `xml:"Tags,omitempty"`
	Web         string   `xml:"Web,omitempty"`
	PageCount   int      `xml:"PageCount,omitempty"`
	LanguageISO string   `xml:"LanguageISO,omitempty"`
	Format      string   `xml:"Format,omitempty"`
	Manga       string   `xml:"Manga,omitempty"`
	SeriesGroup string   `xml:"SeriesGroup,omitempty"`
	AgeRating   string   `xml:"AgeRating,omitempty"`
	// CommunityRating is out of 5, with one decimal.
	CommunityRating string `xml:"CommunityRating,omitempty"`
}

// Input is what the importer knows about a chapter.
type Input struct {
	SeriesTitle      string
	ChapterNumberKey string // canonical number ("12", "10.5")
	Volume           string
	ChapterTitle     string
	Summary          string
	Authors          []string
	Artists          []string
	Scanlator        string
	Publisher        string
	Genres           []string
	Tags             []string
	WebLinks         []string
	PageCount        int
	Language         string
	ReadingDirection string // rtl, ltr, vertical, webtoon
	AgeRating        string
	// Score is the community score out of 10 (0: none).
	Score       float64
	TotalCount  int // set when the series has ended
	ReleaseDate *time.Time
	Special     bool
	Notes       string
}

// Build maps an Input to ComicInfo.
func Build(in Input) ComicInfo {
	ci := ComicInfo{
		XMLNSXsi:    "http://www.w3.org/2001/XMLSchema-instance",
		XMLNSXsd:    "http://www.w3.org/2001/XMLSchema",
		Title:       in.ChapterTitle,
		Series:      in.SeriesTitle,
		Number:      in.ChapterNumberKey,
		Count:       in.TotalCount,
		Summary:     in.Summary,
		Notes:       in.Notes,
		Writer:      joinList(in.Authors),
		Penciller:   joinList(in.Artists),
		Translator:  in.Scanlator,
		Publisher:   in.Publisher,
		Genre:       joinList(in.Genres),
		Tags:        joinList(in.Tags),
		Web:         strings.Join(nonEmpty(in.WebLinks), " "),
		PageCount:   in.PageCount,
		LanguageISO: in.Language,
		AgeRating:   in.AgeRating,
	}
	if in.Score > 0 {
		ci.CommunityRating = strconv.FormatFloat(math.Round(in.Score*5)/10, 'f', 1, 64)
	}
	if v, err := strconv.Atoi(in.Volume); err == nil && v > 0 {
		ci.Volume = v
	}
	if in.ReleaseDate != nil && !in.ReleaseDate.IsZero() {
		ci.Year, ci.Month, ci.Day = in.ReleaseDate.Year(), int(in.ReleaseDate.Month()), in.ReleaseDate.Day()
	}
	switch in.ReadingDirection {
	case "rtl":
		ci.Manga = "YesAndRightToLeft"
	case "ltr", "vertical", "webtoon":
		ci.Manga = "No"
	}
	if in.ReadingDirection == "webtoon" {
		ci.Format = "Webtoon"
	}
	if in.Special {
		ci.Format = "Special"
	}
	return ci
}

// Marshal renders the XML document.
func (c ComicInfo) Marshal() ([]byte, error) {
	b, err := xml.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(b, '\n')...), nil
}

func joinList(xs []string) string { return strings.Join(nonEmpty(xs), ", ") }

func nonEmpty(xs []string) []string {
	out := make([]string, 0, len(xs))
	seen := map[string]bool{}
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x != "" && !seen[strings.ToLower(x)] {
			seen[strings.ToLower(x)] = true
			out = append(out, x)
		}
	}
	return out
}

// AgeRatingFor maps an adult flag / content rating to a ComicInfo AgeRating
// value understood by both Komga and Kavita.
func AgeRatingFor(adult bool, rating string) string {
	switch strings.ToLower(rating) {
	case "everyone", "safe", "all":
		return "Everyone"
	case "teen", "suggestive":
		return "Teen"
	case "mature", "mature 17+", "erotica":
		return "Mature 17+"
	case "adult", "adults only 18+", "pornographic":
		return "Adults Only 18+"
	}
	if adult {
		return "Adults Only 18+"
	}
	return ""
}

// ---- series.json (Mylar schema, read by Komga) --------------------------------

type SeriesJSON struct {
	Version  string       `json:"version"`
	Metadata SeriesJSONMD `json:"metadata"`
}

// SeriesJSONMD contains every field Komga requires; a missing one makes Komga
// silently ignore the whole file.
type SeriesJSONMD struct {
	Type                 string  `json:"type"`
	Publisher            string  `json:"publisher"`
	Imprint              *string `json:"imprint"`
	Name                 string  `json:"name"`
	ComicID              string  `json:"comicid"`
	Year                 int     `json:"year"`
	DescriptionText      string  `json:"description_text"`
	DescriptionFormatted *string `json:"description_formatted"`
	Volume               *int    `json:"volume"`
	BookType             string  `json:"booktype"`
	AgeRating            *string `json:"age_rating"`
	ComicImage           string  `json:"comic_image"`
	TotalIssues          int     `json:"total_issues"`
	PublicationRun       string  `json:"publication_run"`
	Status               string  `json:"status"`
}

type SeriesInput struct {
	Title       string
	ID          string // stable id (e.g. "anilist:30013" or "mangarr:12")
	Publisher   string
	Year        int
	Description string
	CoverURL    string
	TotalCount  int
	Ended       bool
	Adult       bool
	AgeRating   string
}

// BuildSeriesJSON renders series.json bytes.
func BuildSeriesJSON(in SeriesInput) ([]byte, error) {
	status := "Continuing"
	if in.Ended {
		status = "Ended"
	}
	run := ""
	if in.Year > 0 {
		run = strconv.Itoa(in.Year) + " - "
		if in.Ended {
			run = strconv.Itoa(in.Year)
		}
	}
	publisher := in.Publisher
	if publisher == "" {
		publisher = "Unknown"
	}
	md := SeriesJSONMD{
		Type: "comicSeries", Publisher: publisher, Name: in.Title, ComicID: in.ID,
		Year: in.Year, DescriptionText: in.Description, BookType: "Print",
		ComicImage: in.CoverURL, TotalIssues: in.TotalCount, PublicationRun: run, Status: status,
	}
	if ar := mylarAgeRating(in.Adult, in.AgeRating); ar != "" {
		md.AgeRating = &ar
	}
	return json.MarshalIndent(SeriesJSON{Version: "1.0.2", Metadata: md}, "", "  ")
}

func mylarAgeRating(adult bool, rating string) string {
	switch AgeRatingFor(adult, rating) {
	case "Everyone":
		return "All"
	case "Teen":
		return "12+"
	case "Mature 17+":
		return "17+"
	case "Adults Only 18+":
		return "Adult"
	}
	return ""
}
