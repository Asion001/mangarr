package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

// TestWorkerHeartbeatKeepsTheLease: a heartbeat renews the lease whatever
// progress it carries. Processing workers sent only the page counts, the
// server answered 422 for the missing byte counts, and every chapter was
// taken back after one lease and never finished.
func TestWorkerHeartbeatKeepsTheLease(t *testing.T) {
	srv, a := newServer(t, false)
	ctx := context.Background()
	g, _ := a.Settings.General(ctx)
	post := func(path, body, key string) *http.Response {
		r, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Api-Key", key)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post("/api/v1/workers", `{"name":"box","roles":["encode"]}`, g.APIKey)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create worker: %d", resp.StatusCode)
	}
	var made struct {
		Worker struct {
			ID int64 `json:"id"`
		} `json:"worker"`
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&made); err != nil {
		t.Fatal(err)
	}

	jobID := seedDownloadJob(t, a.DB)
	for _, body := range []string{
		`{"pagesDone":0,"pagesTotal":15}`,
		`{"pagesDone":3,"pagesTotal":15,"bytesIn":10,"bytesOut":20}`,
		`{}`,
	} {
		task := &model.WorkerTask{JobID: jobID, Kind: model.TaskEncode}
		if err := a.Tasks.Add(ctx, task); err != nil {
			t.Fatal(err)
		}
		got, err := a.Tasks.Claim(ctx, made.Worker.ID, []string{model.TaskEncode})
		if err != nil || got == nil || got.ID != task.ID {
			t.Fatalf("claim: %+v %v", got, err)
		}
		// pull the lease in, so a renewal shows
		soon := time.Now().UTC().Add(5 * time.Second)
		if _, err := a.DB.NewUpdate().Model((*model.WorkerTask)(nil)).Set("lease_until = ?", soon).
			Where("id = ?", task.ID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		resp := post("/api/v1/worker/tasks/"+strconv.FormatInt(task.ID, 10)+"/heartbeat", body, made.Key)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("heartbeat %s: %d", body, resp.StatusCode)
		}
		var after model.WorkerTask
		if err := a.DB.NewSelect().Model(&after).Where("id = ?", task.ID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if after.LeaseUntil == nil || !after.LeaseUntil.After(soon.Add(time.Minute)) {
			t.Fatalf("heartbeat %s didn't renew the lease: %v", body, after.LeaseUntil)
		}
	}
}
