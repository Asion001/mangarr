package api

import (
	"testing"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
)

// An administrator's chapter list shows only their own progress: another
// reader's page there would read as theirs.
func TestChapterResourcesShowOnlyOwnProgress(t *testing.T) {
	discoverTestDialects(t, func(t *testing.T, e *discoverTestEnv) {
		ser := e.add(t, "Shared", 1)
		var chapter model.Chapter
		if err := e.s.app.DB.NewSelect().Model(&chapter).Where("series_id = ?", ser.ID).Scan(e.ctx); err != nil {
			t.Fatal(err)
		}
		other := &model.User{Username: "someone else", CreatedAt: e.now}
		if _, err := e.s.app.DB.NewInsert().Model(other).Exec(e.ctx); err != nil {
			t.Fatal(err)
		}
		progress := &model.ChapterReadState{UserID: other.ID, ChapterID: chapter.ID, SeriesID: ser.ID, Page: 50, SyncedAt: e.now}
		if err := readstate.Save(e.ctx, e.s.app.DB, progress); err != nil {
			t.Fatal(err)
		}
		readBy := func(p *access.Principal) []ReadStateView {
			t.Helper()
			out, err := e.s.chapterResources(access.With(t.Context(), p), ser.ID)
			if err != nil {
				t.Fatal(err)
			}
			return out[0].ReadBy
		}
		admin := access.AdminPrincipal(access.KindUser)
		admin.UserID = e.reader
		if got := readBy(admin); len(got) != 0 {
			t.Fatalf("admin sees %+v, want no progress", got)
		}
		key := access.AdminPrincipal(access.KindAPIKey)
		if got := readBy(key); len(got) != 1 || got[0].UserID != other.ID || got[0].Page != 50 {
			t.Fatalf("API key sees %+v, want the other reader's page", got)
		}
		admin.UserID = other.ID
		if got := readBy(admin); len(got) != 1 || got[0].Name != "You" {
			t.Fatalf("own progress = %+v", got)
		}
	})
}
