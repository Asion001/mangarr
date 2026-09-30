package series

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

// The language editions of a title show the same cover and facts (status,
// year, people, genres, tags, publisher, rating, format, length, adaptations):
// those of the title's first edition. Only the text is each edition's own:
// its name, description and alternative names. A field the user edited on
// an edition (a lock) stays that edition's.

// sharedFields are the metadata lock and provenance names of what editions share.
var sharedFields = []string{"status", "year", "authors", "artists", "genres", "tags", "publisher", "coverUrl", "ageRating", "format", "adaptations"}

// overlayShared copies the shared info of a title's first edition onto
// another edition and reports whether that changed it.
func overlayShared(dst, first *model.Series) bool {
	before := sharedPrint(dst)
	d, f := &dst.Metadata, first.Metadata
	// what the first edition doesn't know stays as the edition has it
	str := func(field string, to *string, v string) {
		if v != "" && !d.Locked(field) {
			*to = v
		}
	}
	list := func(field string, to *[]string, v []string) {
		if len(v) > 0 && !d.Locked(field) {
			*to = slices.Clone(v)
		}
	}
	if first.Status != "" && first.Status != model.StatusUnknown && !d.Locked("status") {
		dst.Status = first.Status
	}
	if f.Year > 0 && !d.Locked("year") {
		d.Year = f.Year
	}
	list("authors", &d.Authors, f.Authors)
	list("artists", &d.Artists, f.Artists)
	list("genres", &d.Genres, f.Genres)
	list("tags", &d.Tags, f.Tags)
	str("publisher", &d.Publisher, f.Publisher)
	str("coverUrl", &d.CoverURL, f.CoverURL)
	str("ageRating", &d.AgeRating, f.AgeRating)
	str("format", &d.Format, f.Format)
	if f.TotalChapters > 0 {
		d.TotalChapters = f.TotalChapters
	}
	if len(f.Adaptations) > 0 {
		d.Adaptations = slices.Clone(f.Adaptations)
	}
	for _, field := range sharedFields {
		if from, ok := f.Provenance[field]; ok && !d.Locked(field) {
			if d.Provenance == nil {
				d.Provenance = map[string]string{}
			}
			d.Provenance[field] = from
		}
	}
	return sharedPrint(dst) != before
}

// sharedPrint is a comparable form of what overlayShared and a metadata
// refresh can change.
func sharedPrint(s *model.Series) string {
	md := s.Metadata
	md.Provenance = maps.Clone(md.Provenance)
	b, _ := json.Marshal(struct {
		Title, Status, Direction string
		Metadata                 model.SeriesMetadata
	}{s.Title, s.Status, s.ReadingDirection, md})
	return string(b)
}

// firstEdition is the edition the others of ser's title take their shared
// info from, or nil when ser is that edition (or the title's only one).
func (s *Service) firstEdition(ctx context.Context, ser *model.Series) *model.Series {
	if ser.WorkID == 0 || ser.Preview {
		return nil
	}
	var first model.Series
	if err := s.db.NewSelect().Model(&first).Where("work_id = ? AND preview = ?", ser.WorkID, false).Order("id").Limit(1).Scan(ctx); err != nil || first.ID == ser.ID {
		return nil
	}
	return &first
}

// ShareMetadata gives every edition of a work its first edition's shared
// info and cover. It returns how many editions changed.
func (s *Service) ShareMetadata(ctx context.Context, workID int64) (int, error) {
	if workID == 0 {
		return 0, nil
	}
	var editions []model.Series
	if err := s.db.NewSelect().Model(&editions).Where("work_id = ? AND preview = ?", workID, false).Order("id").Scan(ctx); err != nil || len(editions) < 2 {
		return 0, err
	}
	return s.shareMetadata(ctx, editions), nil
}

// ShareAllMetadata is ShareMetadata for every title.
func (s *Service) ShareAllMetadata(ctx context.Context) (int, error) {
	var all []model.Series
	if err := s.db.NewSelect().Model(&all).Where("work_id IS NOT NULL AND preview = ?", false).Order("work_id", "id").Scan(ctx); err != nil {
		return 0, err
	}
	n := 0
	for start := 0; start < len(all); {
		end := start + 1
		for end < len(all) && all[end].WorkID == all[start].WorkID {
			end++
		}
		if end-start > 1 {
			n += s.shareMetadata(ctx, all[start:end])
		}
		start = end
	}
	return n, nil
}

// shareMetadata applies editions[0]'s shared info to the rest.
func (s *Service) shareMetadata(ctx context.Context, editions []model.Series) int {
	first, n := &editions[0], 0
	for i := range editions[1:] {
		ed := &editions[i+1]
		changed := overlayShared(ed, first)
		if !ed.Metadata.Locked("coverUrl") {
			copied, err := s.lib.CopyCover(ctx, first, ed)
			if err != nil {
				s.log.Warn("share cover between editions", "series", ed.Title, "err", err)
			}
			changed = changed || copied
		}
		if !changed {
			continue
		}
		ed.UpdatedAt = time.Now().UTC()
		if _, err := s.db.NewUpdate().Model(ed).Column("status", "metadata", "updated_at").WherePK().Exec(ctx); err != nil {
			s.log.Warn("share metadata between editions", "series", ed.Title, "err", err)
			continue
		}
		n++
		s.bus.Changed("series", "updated", ed.ID)
	}
	return n
}
