package api_test

import (
	"net/http"
	"testing"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/progress"
)

// TestProcessingListsOnlyWorkedOnChapters keeps chapters still waiting for a
// worker out of the processing status's running list.
func TestProcessingListsOnlyWorkedOnChapters(t *testing.T) {
	srv, a := newServer(t, true)
	jobID := seedDownloadJob(t, a.DB)
	a.Downloads.Live.Start(jobID, model.JobKindDownload)
	defer a.Downloads.Live.Finish(jobID)
	active := func() int {
		var st struct {
			Active []struct {
				ID int64 `json:"id"`
			} `json:"active"`
		}
		if code := doJSON(t, http.MethodGet, srv.URL+"/api/v1/processing", "", &st); code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		return len(st.Active)
	}
	a.Downloads.Live.Update(jobID, progress.Event{Stage: progress.StageWait, Total: 10})
	if n := active(); n != 0 {
		t.Fatalf("a waiting chapter is listed: %d", n)
	}
	a.Downloads.Live.Update(jobID, progress.Event{Stage: progress.StageUpscale, Done: 1, Total: 10})
	if n := active(); n != 1 {
		t.Fatalf("an upscaling chapter isn't listed: %d", n)
	}
}
