package app_test

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/modules/notify"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/requests"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// fakeMeta is a metadata provider with fixed series.
type fakeMeta struct{}

var fakeMetaSeries = map[string]metadata.SeriesMetadata{
	"42": {Provider: "fakemeta", ID: "42", Title: "Blue Lock", Year: 2018, ExternalIDs: map[string]string{"mangaupdates": "bl"}},
	"7":  {Provider: "fakemeta", ID: "7", Title: "Kaiju No. 8", Year: 2020},
}

func (fakeMeta) Test(context.Context) error { return nil }
func (fakeMeta) Search(_ context.Context, q string, _ int) ([]metadata.SeriesMetadata, error) {
	var out []metadata.SeriesMetadata
	for _, md := range fakeMetaSeries {
		if strings.Contains(strings.ToLower(md.Title), strings.ToLower(q)) {
			out = append(out, md)
		}
	}
	return out, nil
}
func (fakeMeta) Get(_ context.Context, id string) (*metadata.SeriesMetadata, error) {
	md, ok := fakeMetaSeries[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &md, nil
}

// captured collects messages sent to capture targets, by target name.
var captured = struct {
	sync.Mutex
	m map[string][]notify.Message
}{m: map[string][]notify.Message{}}

type captureNotify struct{ name string }

func (c captureNotify) Test(context.Context) error { return nil }
func (c captureNotify) Send(_ context.Context, msg notify.Message) error {
	captured.Lock()
	defer captured.Unlock()
	captured.m[c.name] = append(captured.m[c.name], msg)
	return nil
}

func capturedFor(name string) []notify.Message {
	captured.Lock()
	defer captured.Unlock()
	return append([]notify.Message(nil), captured.m[name]...)
}

func init() {
	modules.Register(&modules.Implementation{Kind: modules.KindMetadata, Name: "fakemeta", DisplayName: "Fake metadata",
		Settings: func() any { return &struct{}{} },
		New:      func(modules.Deps, any) (modules.Instance, error) { return fakeMeta{}, nil }})
	modules.Register(&modules.Implementation{Kind: modules.KindNotify, Name: "capture", DisplayName: "Capture",
		Settings: func() any { return &struct{}{} },
		New:      func(d modules.Deps, _ any) (modules.Instance, error) { return captureNotify{d.Name}, nil }})
}

func hasTitle(msgs []notify.Message, prefix string) bool {
	for _, m := range msgs {
		if strings.HasPrefix(m.Title, prefix) {
			return true
		}
	}
	return false
}

// TestRequests: friends ask for series; asking twice joins; adding the
// series approves it and the first chapter makes it available, with each
// step on the requesters' own notification targets and the new request on
// the install's.
func TestRequests(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) { testRequests(t, dsn) })
	}
}

func testRequests(t *testing.T, dsn string) {
	captured.Lock()
	captured.m = map[string][]notify.Message{}
	captured.Unlock()
	sc := fakesource.NewScenario("requests")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/bl", Title: "Blue Lock", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
		{URL: "/bl/1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}}})
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/k8", Title: "Kaiju No. 8", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
		{URL: "/k8/1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}}})
	e := newTestApp(t, dsn)
	e.App.Notifications.DigestQuiet = 50 * time.Millisecond
	src := e.addFakeModule(t, "requests")
	meta := &model.ProviderDefinition{Kind: "metadata", Implementation: "fakemeta", Name: "Meta", Enabled: true}
	if err := e.App.Modules.Create(e.Ctx, meta); err != nil {
		t.Fatal(err)
	}
	ctx := e.Ctx
	admin, err := e.App.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"})
	if err != nil {
		t.Fatal(err)
	}
	ann, _ := e.App.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"})
	bob, _ := e.App.Auth.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "bob-pass-1"})
	pBoss, _ := e.App.Auth.UserPrincipal(ctx, admin.ID)
	pAnn, _ := e.App.Auth.UserPrincipal(ctx, ann.ID)
	pBob, _ := e.App.Auth.UserPrincipal(ctx, bob.ID)
	if !pAnn.Can(access.RequestsCreate) || requests.Manages(pAnn) {
		t.Fatalf("Users group permissions %v", pAnn.Perms)
	}
	if err := e.App.Modules.Create(ctx, &model.ProviderDefinition{Kind: "notify", Implementation: "capture", Name: "install", Enabled: true,
		Events: []string{"request.created", "chapter.imported"}}); err != nil {
		t.Fatal(err)
	}
	// Ann and Bob linked Telegram; their chats are named after their phones
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			title, rest, _ := strings.Cut(strings.TrimPrefix(r.Form.Get("text"), "<b>"), "</b>")
			body, _, _ := strings.Cut(strings.TrimPrefix(rest, "\n"), "\n")
			captured.Lock()
			captured.m[r.Form.Get("chat_id")] = append(captured.m[r.Form.Get("chat_id")], notify.Message{Title: html.UnescapeString(title), Body: html.UnescapeString(body)})
			captured.Unlock()
		} else if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			time.Sleep(20 * time.Millisecond)
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
	}))
	t.Cleanup(tg.Close)
	bots := settings.DefaultMessenger()
	bots.Telegram.Enabled, bots.Telegram.BotToken, bots.Telegram.APIURL = true, "token", tg.URL
	if err := e.App.Settings.Set(ctx, settings.KeyMessenger, bots); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	links := []model.MessengerLink{
		{UserID: ann.ID, Kind: model.MessengerTelegram, ExternalID: "ann-phone", Mode: model.DeliveryInstant, Events: events.PersonalEvents, Status: model.LinkActive, CreatedAt: now, UpdatedAt: now},
		{UserID: bob.ID, Kind: model.MessengerTelegram, ExternalID: "bob-phone", Mode: model.DeliveryInstant, Events: []string{events.RequestUpdated}, Status: model.LinkActive, CreatedAt: now, UpdatedAt: now},
	}
	if _, err := e.App.DB.NewInsert().Model(&links).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	// Ann asks; Bob asks for the same one and joins her request
	v, joined, err := e.App.Requests.Create(ctx, pAnn, meta.ID, "42", "please!")
	if err != nil || joined || v.Status != model.RequestPending || v.Title != "Blue Lock" || v.Metadata.ExternalIDs["fakemeta"] != "42" {
		t.Fatalf("create %+v %v %v", v, joined, err)
	}
	waitFor(t, 5*time.Second, "the install hears about the request", func() bool { return hasTitle(capturedFor("install"), "New request: Blue Lock") })
	v2, joined, err := e.App.Requests.Create(ctx, pBob, meta.ID, "42", "")
	if err != nil || !joined || v2.ID != v.ID || v2.Count != 2 {
		t.Fatalf("join %+v %v %v", v2, joined, err)
	}
	if len(v2.Requesters) != 1 || v2.Requesters[0].Name != "bob" {
		t.Fatalf("others' names are shown to a requester: %+v", v2.Requesters)
	}
	all, _ := e.App.Requests.List(ctx, pBoss, requests.Filter{All: true})
	if len(all) != 1 || len(all[0].Requesters) != 2 {
		t.Fatalf("manager's list %+v", all)
	}
	if mine, _ := e.App.Requests.List(ctx, pAnn, requests.Filter{All: true}); len(mine) != 1 || !mine[0].Mine {
		t.Fatalf("ann's list %+v", mine)
	}

	// the boss adds it: approved, and both follow the series
	ser, err := e.App.Series.Add(ctx, series.AddRequest{Metadata: &metadataagg.Ref{ModuleID: meta.ID, Provider: "fakemeta", ID: "42"},
		Sources:      []series.SourceLink{{ModuleID: src, SourceID: "A", URL: "/bl", SourceName: "Source A", Lang: "en"}},
		RootFolderID: e.RFID, Monitor: model.MonitorNone, NoRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.App.Requests.Link(ctx, v.ID, ser.ID, pBoss); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "approved", func() bool {
		r, _ := e.App.Requests.Get(ctx, pBoss, v.ID)
		return r.Status == model.RequestApproved && r.SeriesID != nil && *r.SeriesID == ser.ID && r.HandledBy == "boss"
	})
	waitFor(t, 5*time.Second, "ann hears it was approved", func() bool { return hasTitle(capturedFor("ann-phone"), "Approved: Blue Lock") })
	if n := strings.Count(titles(capturedFor("ann-phone")), "Approved"); n != 1 {
		t.Fatalf("approved told %d times: %s", n, titles(capturedFor("ann-phone")))
	}
	if n, _ := e.App.DB.NewSelect().Model((*model.Follow)(nil)).Where("series_id = ?", ser.ID).Count(ctx); n != 2 {
		t.Fatalf("followers %d", n)
	}
	if hasTitle(capturedFor("install"), "Approved") {
		t.Fatal("a personal message went to the install's target")
	}

	// the first chapter: available, and followers get the new chapter
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	var chs []model.Chapter
	_ = e.App.DB.NewSelect().Model(&chs).Where("series_id = ?", ser.ID).Scan(ctx)
	e.runCommand(t, "SearchMissing", map[string]any{"seriesId": ser.ID, "chapterIds": []int64{chs[0].ID}, "explicit": true})
	waitFor(t, 20*time.Second, "available", func() bool {
		r, _ := e.App.Requests.Get(ctx, pAnn, v.ID)
		return r.Status == model.RequestAvailable
	})
	waitFor(t, 5*time.Second, "ann hears it's available and about the chapter", func() bool {
		m := capturedFor("ann-phone")
		return hasTitle(m, "Available: Blue Lock") && hasTitle(m, "Blue Lock: 1 new chapter")
	})
	waitFor(t, 5*time.Second, "bob hears it's available", func() bool { return hasTitle(capturedFor("bob-phone"), "Available: Blue Lock") })
	if hasTitle(capturedFor("bob-phone"), "Blue Lock: 1 new chapter") {
		t.Fatal("bob's target got chapters though it only wants request updates")
	}

	// asking for something in the library says so
	if _, _, err := e.App.Requests.Create(ctx, pAnn, meta.ID, "42", ""); !errors.As(err, new(requests.AvailableError)) {
		t.Fatalf("request for a series in the library: %v", err)
	}

	// declined, and withdrawn
	k, _, err := e.App.Requests.Create(ctx, pAnn, meta.ID, "7", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.App.Requests.Decline(ctx, k.ID, "not licensed here", pBoss); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "ann hears it was declined", func() bool {
		for _, m := range capturedFor("ann-phone") {
			if m.Title == "Declined: Kaiju No. 8" && strings.Contains(m.Body, "not licensed here") {
				return true
			}
		}
		return false
	})
	if err := e.App.Requests.Withdraw(ctx, k.ID, ann.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Requests.Get(ctx, pBoss, k.ID); err != nil {
		t.Fatalf("a declined request stays for the record: %v", err)
	}
	p, _, _ := e.App.Requests.Create(ctx, pBob, meta.ID, "7", "")
	if err := e.App.Requests.Withdraw(ctx, p.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Requests.Get(ctx, pBoss, p.ID); !errors.Is(err, requests.ErrNotFound) {
		t.Fatalf("a pending request nobody wants any more: %v", err)
	}

	// a group whose requests are added when a source is found automatically
	trusted := &model.Group{Name: "Trusted", Permissions: []string{access.RequestsCreate}, AutoApproveRequests: true, CreatedAt: time.Now()}
	if _, err := e.App.DB.NewInsert().Model(trusted).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	carl, _ := e.App.Auth.CreateUser(ctx, auth.NewUser{Username: "carl", Password: "carl-pass-1", GroupID: trusted.ID})
	pCarl, _ := e.App.Auth.UserPrincipal(ctx, carl.ID)
	auto, _, err := e.App.Requests.Create(ctx, pCarl, meta.ID, "7", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "added automatically", func() bool {
		r, _ := e.App.Requests.Get(ctx, pCarl, auto.ID)
		return r.Status != model.RequestPending && r.SeriesID != nil && r.SeriesTitle == "Kaiju No. 8"
	})
}

func titles(msgs []notify.Message) string {
	var s []string
	for _, m := range msgs {
		s = append(s, m.Title)
	}
	return strings.Join(s, "; ")
}
