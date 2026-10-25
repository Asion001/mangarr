package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/reading"
)

func init() { register((*Server).registerMyReadingStats) }

func (s *Server) registerMyReadingStats() {
	huma.Register(s.api, huma.Operation{OperationID: "me-reading-stats", Method: http.MethodGet, Path: "/api/v1/me/reading-stats", Tags: []string{"Account"},
		Summary: "Active reading time and completed chapter statistics for the current account"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body reading.ReadingStats }, error) {
			// the reader the web reader records time for (readerOf), so
			// without accounts the default reader's time can be seen
			userID, err := s.userOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			stats, err := s.app.Reading.Stats(ctx, userID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body reading.ReadingStats }{stats}, nil
		})
}
