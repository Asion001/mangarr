package dbcopy_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbcopy"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
)

// TestTablesCoverSchema: every table the migrations create is copied.
func TestTablesCoverSchema(t *testing.T) {
	d := dbtest.SQLite(t)
	var names []string
	if err := d.NewSelect().TableExpr("sqlite_master").Column("name").Where("type = 'table'").
		Where("name NOT IN ('sqlite_sequence', 'goose_db_version')").Scan(context.Background(), &names); err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), dbcopy.Tables...)
	sort.Strings(names)
	sort.Strings(want)
	if len(names) != len(want) {
		t.Fatalf("schema tables %v\ncopied tables %v", names, want)
	}
	for i := range names {
		if names[i] != want[i] {
			t.Fatalf("schema tables %v\ncopied tables %v", names, want)
		}
	}
}

// seed fills a database with rows that exercise the conversions: booleans,
// times (and NULL times), JSON, arrays in JSON, foreign keys.
func seed(t *testing.T, d *db.DB) (*model.Series, time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 15, 17, 4, 5, 123456000, time.UTC)
	rf := &model.RootFolder{Path: "/data/manga", Language: "en", CreatedAt: now}
	p := &model.Profile{Name: "Default", IsDefault: true, CreatedAt: now, UpdatedAt: now,
		Config: model.ProfileConfig{PreferredScanlators: []string{"^Official$"}, BlockedScanlators: []string{}}}
	tag := &model.Tag{Label: "keep"}
	for _, m := range []any{rf, p, tag} {
		if _, err := d.NewInsert().Model(m).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ser := &model.Series{Title: "Dr. STONE", SortTitle: "dr. stone", Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll,
		RootFolderID: rf.ID, Path: "Dr. STONE", ProfileID: p.ID, Tags: []int64{tag.ID}, AddedAt: now, UpdatedAt: now,
		Metadata: model.SeriesMetadata{AltTitles: []string{"ドクターストーン"}, Genres: []string{"Sci-Fi"}, Links: map[string]string{"AniList": "https://anilist.co/manga/98416"}}}
	if _, err := d.NewInsert().Model(ser).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		c := &model.Chapter{SeriesID: ser.ID, NumberKey: string(rune('0' + i)), NumberSort: float64(i) + 0.5, Monitored: i != 2,
			State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
		if _, err := d.NewInsert().Model(c).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.NewInsert().Model(&model.Setting{Key: "general", Value: `{"instanceName":"mangarr"}`, UpdatedAt: now}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// accounts, a request and a follow (JSON columns, a NULL series, a
	// composite key)
	g := &model.Group{Name: "Friends", Builtin: "", Permissions: []string{"requests.create", "apps"}, IncludeTags: []int64{tag.ID},
		ExcludeTags: []int64{}, RootFolders: []int64{}, AutoApproveRequests: true, CreatedAt: now}
	if _, err := d.NewInsert().Model(g).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	u := &model.User{Username: "ann", PasswordHash: "", GroupID: g.ID, DisplayName: "Ann", OIDCSubject: "sub-1", CountForCleanup: true, CreatedAt: now}
	if _, err := d.NewInsert().Model(u).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var firstChapter model.Chapter
	if err := d.NewSelect().Model(&firstChapter).Where("series_id = ?", ser.ID).Order("number_sort").Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	req := &model.Request{Title: "Blue Lock", Status: model.RequestPending, CreatedAt: now, UpdatedAt: now,
		Metadata: model.RequestMetadata{ModuleID: 1, Provider: "anilist", ID: "42", Year: 2018, AltTitles: []string{"ブルーロック"},
			ExternalIDs: map[string]string{"anilist": "42"}}}
	if _, err := d.NewInsert().Model(req).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, m := range []any{
		&model.RequestUser{RequestID: req.ID, UserID: u.ID, Note: "please!", CreatedAt: now},
		&model.Follow{UserID: u.ID, SeriesID: ser.ID, CreatedAt: now},
		&model.ReaderPrefs{UserID: u.ID, SeriesID: ser.ID, Data: `{"mode":"webtoon","crop":true}`, UpdatedAt: now},
		&model.ReadingSession{ID: "copy-session-0001", UserID: u.ID, SeriesID: ser.ID, ChapterID: firstChapter.ID,
			ActiveSeconds: 95, StartedAt: now, UpdatedAt: now},
	} {
		if _, err := d.NewInsert().Model(m).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	link := &model.MessengerLink{
		UserID: u.ID, Kind: model.MessengerTelegram, ExternalID: "telegram-123", DisplayName: "Ann",
		Mode: model.DeliveryInstant, Events: []string{"chapter.downloaded"}, Status: model.LinkActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := d.NewInsert().Model(link).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	delivery := &model.NotificationDelivery{
		UserID: u.ID, DedupeKey: "chapter:1:user:1", EventType: "chapter.downloaded", SeriesID: &ser.ID,
		Payload: map[string]any{"title": ser.Title, "chapters": []any{"1.5"}}, CreatedAt: now,
	}
	for _, m := range []any{
		&model.MessengerLinkToken{TokenHash: "sha256:copy-token", UserID: u.ID, Kind: model.MessengerTelegram, ExpiresAt: now.Add(time.Minute), CreatedAt: now},
		delivery,
	} {
		if _, err := d.NewInsert().Model(m).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.NewInsert().Model(&model.NotificationDispatch{
		DeliveryID: delivery.ID, LinkID: link.ID, AvailableAt: now,
	}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var chapters []model.Chapter
	if err := d.NewSelect().Model(&chapters).Where("series_id = ?", ser.ID).Order("id").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{model.JobKindDownload, model.JobKindReprocess} {
		job := &model.DownloadJob{Kind: kind, SeriesID: ser.ID, ChapterID: chapters[i].ID, Status: model.JobQueued,
			Rank: int64(2*i-1) * 1048576, NotBefore: now, CreatedAt: now, UpdatedAt: now}
		if _, err := d.NewInsert().Model(job).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.ExecContext(ctx, "UPDATE download_queue_order SET revision = 7 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	return ser, now
}

func check(t *testing.T, d *db.DB, want *model.Series, now time.Time) {
	t.Helper()
	ctx := context.Background()
	var got model.Series
	if err := d.NewSelect().Model(&got).Where("id = ?", want.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if got.Title != want.Title || !got.Monitored || !got.AddedAt.Equal(now) || got.LastMetadataRefresh != nil ||
		len(got.Tags) != 1 || got.Metadata.AltTitles[0] != "ドクターストーン" || got.Metadata.Links["AniList"] == "" {
		t.Fatalf("series %+v", got)
	}
	var chs []model.Chapter
	_ = d.NewSelect().Model(&chs).Where("series_id = ?", want.ID).Order("number_sort").Scan(ctx)
	if len(chs) != 3 || chs[1].Monitored || !chs[2].Monitored || chs[0].NumberSort != 1.5 {
		t.Fatalf("chapters %+v", chs)
	}
	var p model.Profile
	_ = d.NewSelect().Model(&p).Limit(1).Scan(ctx)
	if !p.IsDefault || len(p.Config.PreferredScanlators) != 1 {
		t.Fatalf("profile %+v", p)
	}
	var req model.Request
	if err := d.NewSelect().Model(&req).Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if req.Status != model.RequestPending || req.SeriesID != nil || req.Metadata.ExternalIDs["anilist"] != "42" ||
		len(req.Metadata.AltTitles) != 1 || !req.CreatedAt.Equal(now) {
		t.Fatalf("request %+v", req)
	}
	var grp model.Group
	_ = d.NewSelect().Model(&grp).Where("name = ?", "Friends").Scan(ctx)
	if !grp.AutoApproveRequests || len(grp.Permissions) != 2 || len(grp.IncludeTags) != 1 {
		t.Fatalf("group %+v", grp)
	}
	var prefs model.ReaderPrefs
	_ = d.NewSelect().Model(&prefs).Limit(1).Scan(ctx)
	if !strings.Contains(prefs.Data, "webtoon") {
		t.Fatalf("reader settings %+v", prefs)
	}
	var session model.ReadingSession
	if err := d.NewSelect().Model(&session).Where("id = ?", "copy-session-0001").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if session.SeriesID != want.ID || session.ActiveSeconds != 95 || !session.UpdatedAt.Equal(now) {
		t.Fatalf("reading session %+v", session)
	}
	if n, _ := d.NewSelect().Model((*model.Follow)(nil)).Count(ctx); n != 1 {
		t.Fatalf("follows %d", n)
	}
	if n, _ := d.NewSelect().Model((*model.RequestUser)(nil)).Count(ctx); n != 1 {
		t.Fatalf("requesters %d", n)
	}
	var link model.MessengerLink
	if err := d.NewSelect().Model(&link).Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if link.ExternalID != "telegram-123" || len(link.Events) != 1 || link.Events[0] != "chapter.downloaded" {
		t.Fatalf("messenger link %+v", link)
	}
	var delivery model.NotificationDelivery
	if err := d.NewSelect().Model(&delivery).Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if delivery.SeriesID == nil || *delivery.SeriesID != want.ID || delivery.Payload["title"] != want.Title {
		t.Fatalf("notification delivery %+v", delivery)
	}
	if n, _ := d.NewSelect().Model((*model.MessengerLinkToken)(nil)).Count(ctx); n != 1 {
		t.Fatalf("messenger link tokens %d", n)
	}
	if n, _ := d.NewSelect().Model((*model.NotificationDispatch)(nil)).Count(ctx); n != 1 {
		t.Fatalf("notification dispatches %d", n)
	}
	var jobs []model.DownloadJob
	if err := d.NewSelect().Model(&jobs).Order("rank").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].Rank != -1048576 || jobs[1].Rank != 1048576 ||
		jobs[0].Kind != model.JobKindDownload || jobs[1].Kind != model.JobKindReprocess {
		t.Fatalf("queue ranks not preserved: %+v", jobs)
	}
	var revision int64
	if err := d.NewSelect().Table("download_queue_order").Column("revision").Where("id = 1").Scan(ctx, &revision); err != nil {
		t.Fatal(err)
	}
	if revision != 7 {
		t.Fatalf("queue revision: %d", revision)
	}
	// new rows get new ids after the copied ones
	extra := &model.Tag{Label: "new in " + string(d.Kind)}
	if _, err := d.NewInsert().Model(extra).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if extra.ID <= 1 {
		t.Fatalf("id sequence not moved: %d", extra.ID)
	}
}

// TestCopy moves a seeded SQLite database to Postgres and back.
func TestCopy(t *testing.T) {
	ctx := context.Background()
	src := dbtest.SQLite(t)
	ser, now := seed(t, src)
	pg := dbtest.Postgres(t)
	var events []string
	res, err := dbcopy.Copy(ctx, src, pg, false, func(table string, done, total int) {
		if done == total && total > 0 {
			events = append(events, table)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows["chapters"] != 3 || res.Rows["series"] != 1 || res.Total < 9 || len(events) == 0 {
		t.Fatalf("result %+v %v", res, events)
	}
	check(t, pg, ser, now)

	// a non-empty target needs overwrite
	if _, err := dbcopy.Copy(ctx, src, pg, false, nil); !errors.Is(err, dbcopy.ErrNotEmpty) {
		t.Fatalf("copy into a used database: %v", err)
	}

	// and back into a fresh SQLite file
	back := dbtest.SQLite(t)
	if _, err := dbcopy.Copy(ctx, pg, back, false, nil); err != nil {
		t.Fatal(err)
	}
	check(t, back, ser, now)
	if _, err := dbcopy.Copy(ctx, pg, back, true, nil); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
}

// TestCopySQLite: SQLite to SQLite (backups of a SQLite install use it too).
func TestCopySQLite(t *testing.T) {
	src := dbtest.SQLite(t)
	ser, now := seed(t, src)
	dst := dbtest.SQLite(t)
	if _, err := dbcopy.Copy(context.Background(), src, dst, false, nil); err != nil {
		t.Fatal(err)
	}
	check(t, dst, ser, now)
}
