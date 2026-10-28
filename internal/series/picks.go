package series

import (
	"context"
	"slices"
	"time"

	"github.com/Asion001/mangarr/internal/genres"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/naming"
)

// MetadataPicks are fields the user took from one provider for a title.
// A picked field is locked, so a refresh keeps it.
type MetadataPicks struct {
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	CoverURL    *string  `json:"coverUrl,omitempty"`
	Status      *string  `json:"status,omitempty" enum:"unknown,ongoing,completed,hiatus,cancelled"`
	Year        *int     `json:"year,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// From names the provider of each picked field (field -> provider).
	From map[string]string `json:"from,omitempty"`
	// Automatic are fields handed back to the providers: unlocked, then
	// the metadata is refreshed.
	Automatic []string `json:"automatic,omitempty" enum:"title,description,coverUrl,status,year,genres,tags"`
}

// sharedPicks are the picked fields every edition of a title shares.
var sharedPicks = []string{"coverUrl", "status", "year", "genres", "tags"}

// PickMetadata applies picks to a series. The shared fields go to the
// title's first edition too, which hands them to the others.
func (s *Service) PickMetadata(ctx context.Context, id int64, p MetadataPicks) (*model.Series, error) {
	ser, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	targets := []*model.Series{ser}
	if first := s.firstEdition(ctx, ser); first != nil {
		targets = append(targets, first)
	}
	now := time.Now().UTC()
	for i, t := range targets {
		oldCover := t.Metadata.CoverURL
		md := &t.Metadata
		pick := func(field string) bool {
			if i > 0 && !slices.Contains(sharedPicks, field) {
				return false
			}
			if !md.Locked(field) {
				md.Locks = append(md.Locks, field)
			}
			if md.Provenance == nil {
				md.Provenance = map[string]string{}
			}
			if from := p.From[field]; from != "" {
				md.Provenance[field] = from
			}
			return true
		}
		if p.Title != nil && *p.Title != "" && pick("title") {
			t.Title = *p.Title
			t.SortTitle = naming.SortTitle(t.Title)
		}
		if p.Description != nil && pick("description") {
			md.Description = *p.Description
		}
		if p.CoverURL != nil && *p.CoverURL != "" && pick("coverUrl") {
			md.CoverURL = *p.CoverURL
		}
		if p.Status != nil && *p.Status != "" && pick("status") {
			t.Status = *p.Status
		}
		if p.Year != nil && *p.Year > 0 && pick("year") {
			md.Year = *p.Year
		}
		if p.Genres != nil && pick("genres") {
			md.Genres = cleanNames(genres.Normalize(p.Genres))
		}
		if p.Tags != nil && pick("tags") {
			md.Tags = cleanNames(genres.Normalize(p.Tags))
		}
		for _, field := range p.Automatic {
			if i > 0 && !slices.Contains(sharedPicks, field) {
				continue
			}
			md.Locks = slices.DeleteFunc(md.Locks, func(l string) bool { return l == field })
		}
		t.UpdatedAt = now
		if _, err := s.db.NewUpdate().Model(t).WherePK().Exec(ctx); err != nil {
			return nil, err
		}
		if len(p.Automatic) > 0 {
			if _, err := s.RefreshMetadata(ctx, t.ID); err != nil {
				return nil, err
			}
		} else if oldCover != md.CoverURL {
			_ = s.lib.RefreshCover(ctx, t, nil)
		}
	}
	if _, err := s.ShareMetadata(ctx, ser.WorkID); err != nil {
		return nil, err
	}
	s.bus.Changed("series", "updated", ser.ID)
	return s.Get(ctx, ser.ID)
}
