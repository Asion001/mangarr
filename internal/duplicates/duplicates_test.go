package duplicates

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/settings"
)

// fakeSeries groups and links in the database the way the series service does.
type fakeSeries struct {
	d      *db.DB
	linked map[int64]metadataagg.Ref
}

func (f *fakeSeries) Get(ctx context.Context, id int64) (*model.Series, error) {
	var s model.Series
	return &s, f.d.NewSelect().Model(&s).Where("id = ?", id).Scan(ctx)
}

func (f *fakeSeries) SetWork(ctx context.Context, seriesID, workID int64) error {
	_, err := f.d.NewUpdate().Model((*model.Series)(nil)).Set("work_id = ?", workID).Where("id = ?", seriesID).Exec(ctx)
	return err
}

func (f *fakeSeries) LinkMetadata(ctx context.Context, id int64, ref metadataagg.Ref) (*model.Series, error) {
	f.linked[id] = ref
	s, err := f.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.Metadata.ExternalIDs = map[string]string{ref.Provider: ref.ID}
	_, err = f.d.NewUpdate().Model(s).WherePK().Exec(ctx)
	return s, err
}

func (f *fakeSeries) AddMetadataRef(ctx context.Context, id int64, ref metadataagg.Ref) (*model.Series, error) {
	f.linked[id] = ref
	s, err := f.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.Metadata.ExternalIDs[ref.Provider] = ref.ID
	_, err = f.d.NewUpdate().Model(s).WherePK().Exec(ctx)
	return s, err
}

func (f *fakeSeries) Merge(ctx context.Context, keepID, dropID int64) error {
	_, err := f.d.NewDelete().Model((*model.Series)(nil)).Where("id = ?", dropID).Exec(ctx)
	return err
}

func candidate(provider, id, title string, ids map[string]string) *metadataagg.Candidate {
	return &metadataagg.Candidate{ModuleID: 1, SeriesMetadata: metadata.SeriesMetadata{Provider: provider, ID: id, Title: title, ExternalIDs: ids}}
}

func TestScan(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
		root := &model.RootFolder{Path: "/manga", Language: "en", CreatedAt: now}
		profile := &model.Profile{Name: "Default", IsDefault: true, Config: model.ProfileConfig{}, CreatedAt: now, UpdatedAt: now}
		for _, item := range []any{root, profile} {
			if _, err := d.NewInsert().Model(item).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		add := func(title, lang string, md model.SeriesMetadata, source string) int64 {
			t.Helper()
			w := &model.Work{Title: title, SortTitle: title, CreatedAt: now, UpdatedAt: now}
			if _, err := d.NewInsert().Model(w).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			s := &model.Series{WorkID: w.ID, Title: title, SortTitle: title, Status: model.StatusOngoing, MonitorNew: model.MonitorAll,
				RootFolderID: root.ID, Path: title + " (" + lang + ")", ProfileID: profile.ID, Language: lang, SourcePriorityMode: "custom", ReadingDirection: "rtl",
				Tags: []int64{}, Metadata: md, BlockedScanlators: []string{}, AddedAt: now, UpdatedAt: now}
			if _, err := d.NewInsert().Model(s).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if source != "" {
				src := &model.SeriesSource{SeriesID: s.ID, ModuleID: 1, SourceID: "x", SourceName: source, Lang: lang, MangaURL: "/manga/" + title,
					Title: title, NextCheckAt: now, CreatedAt: now}
				if _, err := d.NewInsert().Model(src).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			return s.ID
		}
		tbate := add("The Beginning After the End", "en", model.SeriesMetadata{}, "MangaDex")
		nachalo := add("Начало после конца", "ru", model.SeriesMetadata{}, "Senkuro")
		berserk := add("Berserk", "en", model.SeriesMetadata{ExternalIDs: map[string]string{"anilist": "1"}}, "")
		berserkRU := add("Берсерк", "ru", model.SeriesMetadata{ExternalIDs: map[string]string{"anilist": "1"}}, "")
		solo := add("Solo Leveling", "ru", model.SeriesMetadata{}, "Senkuro")
		soloRU := add("Поднятие уровня в одиночку", "ru", model.SeriesMetadata{AltTitles: []string{"Solo Leveling"}, ExternalIDs: map[string]string{"anilist": "5"}}, "MangaLib")
		unknown := add("Мой сосед-вампир", "ru", model.SeriesMetadata{}, "")
		// a different work by the same name at the providers
		add("Monster", "en", model.SeriesMetadata{ExternalIDs: map[string]string{"anilist": "7"}}, "")
		add("Monster", "ru", model.SeriesMetadata{ExternalIDs: map[string]string{"anilist": "8"}}, "")

		lookups := []string{}
		fake := &fakeSeries{d: d, linked: map[int64]metadataagg.Ref{}}
		svc := &Service{DB: d, Series: fake, Settings: settings.NewStore(d), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Lookup: func(ctx context.Context, title string, alts []string) (*metadataagg.Candidate, float64) {
				lookups = append(lookups, title)
				switch title {
				case "The Beginning After the End":
					return candidate("anilist", "100", "The Beginning After the End", map[string]string{"anilist": "100", "mal": "50"}), 1
				case "Начало после конца":
					return candidate("shikimori", "50", "The Beginning After the End", map[string]string{"shikimori": "50", "mal": "50"}), 0.93
				case "Solo Leveling":
					return candidate("anilist", "5", "Solo Leveling", map[string]string{"anilist": "5"}), 0.95
				}
				return nil, 0
			}}
		res, err := svc.Scan(ctx, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.LookedUp != 4 || res.Found != 3 || res.Grouped != 2 || res.Linked != 0 {
			t.Fatalf("result %+v", res)
		}
		workOf := func(id int64) int64 {
			s, _ := fake.Get(ctx, id)
			return s.WorkID
		}
		// the import's Russian edition joins the English title, both get the metadata
		if workOf(nachalo) != workOf(tbate) || workOf(berserkRU) != workOf(berserk) {
			t.Fatal("other-language duplicates were not grouped")
		}
		if _, ok := fake.linked[tbate]; !ok {
			t.Fatal("tbate not linked")
		}
		if _, ok := fake.linked[nachalo]; !ok {
			t.Fatal("nachalo not linked")
		}
		list, err := svc.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var soloPair, unknownMeta, soloMeta *Finding
		for i, f := range list {
			switch {
			case f.Kind == KindPair && f.SeriesA == solo && f.SeriesB == soloRU:
				soloPair = &list[i]
			case f.Kind == KindMetadata && f.SeriesA == unknown:
				unknownMeta = &list[i]
			case f.Kind == KindMetadata && f.SeriesA == solo:
				soloMeta = &list[i]
			case f.Kind == KindPair && f.State == StateOpen:
				t.Errorf("unexpected open pair %d/%d %+v", f.SeriesA, f.SeriesB, f.Reasons)
			}
		}
		// same language is only ever merged on request; the one with metadata is kept
		if soloPair == nil || soloPair.State != StateOpen || !soloPair.SameLanguage || !soloPair.Sure || soloPair.KeepID != soloRU {
			t.Fatalf("solo pair %+v", soloPair)
		}
		if unknownMeta == nil || unknownMeta.State != StateNone {
			t.Fatalf("unknown %+v", unknownMeta)
		}
		// not sure enough to link by itself
		if soloMeta == nil || soloMeta.State != StateOpen || soloMeta.Sure || soloMeta.Match.ID != "5" {
			t.Fatalf("solo metadata %+v", soloMeta)
		}
		n, err := svc.Link(ctx, KindMetadata, []int64{solo, unknown})
		if err != nil || n != 1 || fake.linked[solo].ID != "5" {
			t.Fatalf("link: %d %v %v", n, err, fake.linked[solo])
		}
		if err := svc.SetDismissed(ctx, soloPair.ID, true); err != nil {
			t.Fatal(err)
		}
		if err := svc.SetDismissed(ctx, soloPair.ID, false); err != nil {
			t.Fatal(err)
		}
		if err := svc.Merge(ctx, soloPair.ID, berserk); err == nil {
			t.Fatal("merged into a title outside the pair")
		}
		if err := svc.SetDismissed(ctx, soloPair.ID, true); err != nil {
			t.Fatal(err)
		}

		// a second scan asks nobody again and keeps the decisions
		lookups = nil
		if _, err := svc.Scan(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
		if len(lookups) != 0 {
			t.Fatalf("looked up again: %v", lookups)
		}
		if f, _ := svc.get(ctx, soloPair.ID); f == nil || f.State != StateDismissed {
			t.Fatalf("dismissed pair came back: %+v", f)
		}
		// asking again for one title looks just that one up
		if _, err := svc.Scan(ctx, []int64{unknown}, nil); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(lookups, []string{"Мой сосед-вампир"}) {
			t.Fatalf("lookups %v", lookups)
		}

		// with MangaUpdates primary, titles linked to AniList only are looked
		// up there, by the ids they have first
		var asked []string
		svc.Primary = func() string { return "mangaupdates" }
		svc.AtPrimary = func(ctx context.Context, k metadataagg.Known) (*metadataagg.Candidate, string, string, float64) {
			asked = append(asked, k.Names[0])
			if k.IDs["anilist"] == "1" {
				return candidate("mangaupdates", "mu1", "Berserk", nil), metadataagg.FoundByID, "anilist", 1
			}
			return nil, "", "", 0
		}
		if _, err := svc.Scan(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
		list, _ = svc.List(ctx)
		var berserkAt *Finding
		primaries := 0
		for i, f := range list {
			if f.Kind == KindPrimary {
				primaries++
				if f.SeriesA == berserk {
					berserkAt = &list[i]
				}
			}
		}
		if berserkAt == nil || berserkAt.State != StateOpen || berserkAt.Match.How != "id" || berserkAt.Match.By != "anilist" || !berserkAt.Sure {
			t.Fatalf("berserk at the primary provider: %+v (asked %v)", berserkAt, asked)
		}
		if n, err := svc.Link(ctx, KindPrimary, []int64{berserk}); err != nil || n != 1 {
			t.Fatalf("move: %d %v", n, err)
		}
		if s, _ := fake.Get(ctx, berserk); s.Metadata.ExternalIDs["anilist"] != "1" || s.Metadata.ExternalIDs["mangaupdates"] != "mu1" {
			t.Fatalf("moved ids %v", s.Metadata.ExternalIDs)
		}
		// moved titles drop off the next scan, the others are not asked again
		asked = nil
		if _, err := svc.Scan(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
		list, _ = svc.List(ctx)
		after := 0
		for _, f := range list {
			if f.Kind == KindPrimary {
				after++
				if f.SeriesA == berserk && f.State != StateDone {
					t.Fatalf("moved title still listed: %+v", f)
				}
			}
		}
		if len(asked) != 0 || after > primaries {
			t.Fatalf("second scan asked %v, %d findings (was %d)", asked, after, primaries)
		}
	})
}
