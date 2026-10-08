package anilist

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Asion001/mangarr/internal/modules/metadata"
)

// recommendationFields asks for a title's manga relations and its users'
// recommendations, each with what a search result has.
const recommendationFields = `relations { edges { relationType node { type ` + searchFields + ` } } }
recommendations(sort: RATING_DESC, perPage: 20) { nodes { rating mediaRecommendation { type ` + searchFields + ` } } }`

// Recommendations implements metadata.Recommender.
func (m *Module) Recommendations(ctx context.Context, id string) (*metadata.Recommendations, error) {
	n, err := strconv.Atoi(id)
	if err != nil {
		return nil, fmt.Errorf("invalid AniList id %q", id)
	}
	var out struct {
		Media media `json:"Media"`
	}
	if err := m.query(ctx, `query ($id: Int) { Media(id: $id, type: MANGA) { id `+recommendationFields+` } }`, map[string]any{"id": n}, &out); err != nil {
		return nil, err
	}
	return m.recommendations(out.Media), nil
}

func (m *Module) recommendations(md media) *metadata.Recommendations {
	res := &metadata.Recommendations{Related: []metadata.Related{}, Recommended: []metadata.Recommended{}}
	seen := map[int]bool{md.ID: true}
	if md.Relations != nil {
		for _, edge := range md.Relations.Edges {
			node := edge.Node
			// anime are adaptations; characters and "other" aren't the story
			if node == nil || node.Type != "MANGA" || node.ID <= 0 || seen[node.ID] || edge.RelationType == "CHARACTER" || edge.RelationType == "OTHER" {
				continue
			}
			seen[node.ID] = true
			res.Related = append(res.Related, metadata.Related{SeriesMetadata: m.convert(*node), Relation: strings.ToLower(edge.RelationType)})
		}
	}
	if md.Recommendations != nil {
		for _, rec := range md.Recommendations.Nodes {
			node := rec.MediaRecommendation
			if node == nil || node.ID <= 0 || seen[node.ID] || rec.Rating <= 0 || (node.Type != "" && node.Type != "MANGA") {
				continue
			}
			seen[node.ID] = true
			res.Recommended = append(res.Recommended, metadata.Recommended{SeriesMetadata: m.convert(*node), Votes: rec.Rating})
		}
	}
	return res
}

var _ metadata.Recommender = (*Module)(nil)
