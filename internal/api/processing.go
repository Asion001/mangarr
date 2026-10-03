package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/processing"
	"github.com/Asion001/mangarr/internal/progress"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/upscaling"
)

func init() { register((*Server).registerProcessing) }

type EngineInfo struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Slow   bool   `json:"slow"`
}

type ProcessingStatus struct {
	Engines []EngineInfo     `json:"engines"`
	State   processing.State `json:"state"`
	// Pending counts files waiting for background processing; Failed gave up.
	Pending    int   `json:"pending"`
	Failed     int   `json:"failed"`
	Processed  int   `json:"processed"`
	SpaceSaved int64 `json:"spaceSaved"`
	// SpaceAdded counts growth where the original size is known; SpaceSaved
	// remains gross savings for existing clients. NetSpaceSaved can be negative.
	SpaceAdded    int64 `json:"spaceAdded"`
	NetSpaceSaved int64 `json:"netSpaceSaved"`
	// Active are the jobs processing right now, with live progress.
	Active []downloads.JobView `json:"active"`
	// PagesPerMinute is the processing speed over the last day (0 = unknown).
	PagesPerMinute float64 `json:"pagesPerMinute"`
	// PendingPages sums the pages of pending files; ETASeconds estimates the
	// time to process them at PagesPerMinute.
	PendingPages int     `json:"pendingPages"`
	ETASeconds   float64 `json:"etaSeconds"`
	// Recent are the last processed files.
	Recent []ProcessedFile `json:"recent"`
}

// ProcessedFile is one processed chapter file.
type ProcessedFile struct {
	SeriesID     int64     `json:"seriesId"`
	SeriesTitle  string    `json:"seriesTitle"`
	Chapter      string    `json:"chapter"`
	SizeOriginal int64     `json:"sizeOriginal"`
	Size         int64     `json:"size"`
	Pages        int       `json:"pages"`
	Seconds      float64   `json:"seconds"`
	ProcessedAt  time.Time `json:"processedAt"`
}

// ProcessingDay aggregates one day of processing.
type ProcessingDay struct {
	Day         string  `json:"day"` // YYYY-MM-DD, server time
	Files       int     `json:"files"`
	Pages       int     `json:"pages"`
	BytesBefore int64   `json:"bytesBefore"`
	BytesAfter  int64   `json:"bytesAfter"`
	Seconds     float64 `json:"seconds"`
}

type PreviewPage struct {
	Index          int    `json:"index"`
	Name           string `json:"name"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	OriginalFormat string `json:"originalFormat"`
	OriginalSize   int64  `json:"originalSize"`
	// Encoded* and Result* describe the page as it would be stored, after
	// upscaling and re-encoding.
	EncodedFormat string `json:"encodedFormat"`
	EncodedSize   int64  `json:"encodedSize"`
	ResultWidth   int    `json:"resultWidth"`
	ResultHeight  int    `json:"resultHeight"`
	Upscaled      bool   `json:"upscaled" doc:"The page was narrower than the threshold and went through the upscaler"`
	Shrunk        bool   `json:"shrunk" doc:"The page was wider than the profile allows and was downsized"`
	Split         bool   `json:"split" doc:"The result is one segment of a tall source page"`
	Junk          bool   `json:"junk" doc:"The image is under the junk size and is left alone"`
}

type PreviewResult struct {
	Token string `json:"token"`
	// Engine is the encoder; empty when pages aren't re-encoded.
	Engine string `json:"engine"`
	// Upscaler is the model the upscaled pages ran with; empty when none was.
	Upscaler       string        `json:"upscaler"`
	UpscaleSeconds float64       `json:"upscaleSeconds"`
	EncodeSeconds  float64       `json:"encodeSeconds"`
	Seconds        float64       `json:"seconds"`
	Pages          []PreviewPage `json:"pages"`
}

type preview struct {
	dir     string
	created time.Time
	orig    []string
	enc     []string
}

var (
	previewMu sync.Mutex
	previews  = map[string]*preview{}
)

func (s *Server) cleanPreviews() {
	previewMu.Lock()
	defer previewMu.Unlock()
	for k, p := range previews {
		if time.Since(p.created) > time.Hour {
			os.RemoveAll(p.dir)
			delete(previews, k)
		}
	}
}

// processingActivity fills in running jobs, speed, ETA and recent files.
func (s *Server) processingActivity(ctx context.Context, st *ProcessingStatus) {
	st.Active, st.Recent = []downloads.JobView{}, []ProcessedFile{}
	var ids []int64
	live := map[int64]downloads.LiveProgress{}
	for _, lp := range s.app.Downloads.Live.All() {
		// only chapters something is working on: ones still waiting for a
		// worker are counted under Waiting
		working := lp.Stage == progress.StageUpscale || lp.Stage == progress.StageSplit || lp.Stage == progress.StageEncode
		if working || (lp.Kind == model.JobKindReprocess && lp.Stage != "" && lp.Stage != progress.StageWait) {
			ids = append(ids, lp.JobID)
			live[lp.JobID] = lp
		}
	}
	if len(ids) > 0 {
		if p, err := s.app.DLQueue.ListPage(ctx, downloads.ListFilter{IDs: ids, IncludeDone: true}, 1, 100); err == nil {
			for _, j := range p.Items {
				lp := live[j.ID]
				j.Live = &lp
				st.Active = append(st.Active, j)
			}
		}
	}
	var speed struct {
		Pages   int     `bun:"pages"`
		Seconds float64 `bun:"seconds"`
	}
	_ = s.app.DB.NewSelect().Model((*model.ChapterFile)(nil)).
		ColumnExpr("COALESCE(SUM(process_pages), 0) AS pages, COALESCE(SUM(process_seconds), 0) AS seconds").
		Where("processed_at > ? AND process_seconds > 0", time.Now().UTC().Add(-24*time.Hour)).Scan(ctx, &speed)
	if speed.Seconds > 0 && speed.Pages > 0 {
		st.PagesPerMinute = float64(speed.Pages) / speed.Seconds * 60
		st.ETASeconds = float64(st.PendingPages) / st.PagesPerMinute * 60
	}
	_ = s.app.DB.NewSelect().TableExpr("chapter_files AS f").
		Join("JOIN series AS s ON s.id = f.series_id").Join("JOIN chapters AS c ON c.id = f.chapter_id").
		ColumnExpr("f.series_id AS series_id, s.title AS series_title, c.number_key AS chapter, f.size_original AS size_original").
		ColumnExpr("f.size AS size, f.process_pages AS pages, f.process_seconds AS seconds, f.processed_at AS processed_at").
		Where("f.processed_at IS NOT NULL AND f.process_seconds > 0").OrderExpr("f.processed_at DESC").Limit(10).Scan(ctx, &st.Recent)
}

// processingHistory aggregates processed files per day.
func (s *Server) processingHistory(ctx context.Context, days int) ([]ProcessingDay, error) {
	since := time.Now().AddDate(0, 0, -days+1)
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.Local)
	var rows []struct {
		ProcessedAt  time.Time `bun:"processed_at"`
		SizeOriginal int64     `bun:"size_original"`
		Size         int64     `bun:"size"`
		Pages        int       `bun:"process_pages"`
		Seconds      float64   `bun:"process_seconds"`
	}
	if err := s.app.DB.NewSelect().Model((*model.ChapterFile)(nil)).
		Column("processed_at", "size_original", "size", "process_pages", "process_seconds").
		Where("processed_at >= ? AND process_state = ?", since.UTC(), model.ProcessDone).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	byDay := map[string]*ProcessingDay{}
	out := make([]ProcessingDay, days)
	for i := range out {
		d := since.AddDate(0, 0, i).Format("2006-01-02")
		out[i].Day = d
		byDay[d] = &out[i]
	}
	for _, r := range rows {
		d := byDay[r.ProcessedAt.Local().Format("2006-01-02")]
		if d == nil {
			continue
		}
		before := r.SizeOriginal
		if before <= 0 {
			before = r.Size
		}
		d.Files++
		d.Pages += r.Pages
		d.BytesBefore += before
		d.BytesAfter += r.Size
		d.Seconds += r.Seconds
	}
	return out, nil
}

func (s *Server) registerProcessing() {
	tags := []string{"Processing"}
	huma.Register(s.api, huma.Operation{OperationID: "processing-status", Method: http.MethodGet, Path: "/api/v1/processing", Tags: tags,
		Summary: "Encoders, background backlog and space saved"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body ProcessingStatus }, error) {
			st := ProcessingStatus{Engines: []EngineInfo{}}
			for _, e := range s.app.Encoder.Engines() {
				st.Engines = append(st.Engines, EngineInfo{Name: e.Name(), Format: e.Format(), Slow: e.Slow()})
			}
			st.State = s.app.Processing.Guard.State(ctx)
			var profiles []model.Profile
			_ = s.app.DB.NewSelect().Model(&profiles).Scan(ctx)
			for _, p := range profiles {
				params := p.Config.ProcessParams()
				if params == "" {
					continue
				}
				base := s.app.DB.NewSelect().Model((*model.ChapterFile)(nil)).Where("process_params <> ?", params).
					Where("series_id IN (SELECT id FROM series WHERE profile_id = ?)", p.ID)
				n, _ := base.Where("process_attempts < ?", downloads.MaxProcessAttempts).Count(ctx)
				st.Pending += n
				var pages int
				_ = s.app.DB.NewSelect().Model((*model.ChapterFile)(nil)).ColumnExpr("COALESCE(SUM(page_count), 0)").
					Where("process_params <> ?", params).Where("series_id IN (SELECT id FROM series WHERE profile_id = ?)", p.ID).
					Where("process_attempts < ?", downloads.MaxProcessAttempts).Scan(ctx, &pages)
				st.PendingPages += pages
				f, _ := s.app.DB.NewSelect().Model((*model.ChapterFile)(nil)).Where("process_params <> ?", params).
					Where("series_id IN (SELECT id FROM series WHERE profile_id = ?)", p.ID).
					Where("process_attempts >= ?", downloads.MaxProcessAttempts).Count(ctx)
				st.Failed += f
			}
			var agg struct {
				Processed int   `bun:"processed"`
				Saved     int64 `bun:"saved"`
				Added     int64 `bun:"added"`
			}
			_ = s.app.DB.NewSelect().Model((*model.ChapterFile)(nil)).
				ColumnExpr("SUM(CASE WHEN process_state = ? THEN 1 ELSE 0 END) AS processed", model.ProcessDone).
				ColumnExpr("COALESCE(SUM(CASE WHEN size_original > size THEN size_original - size ELSE 0 END), 0) AS saved").
				ColumnExpr("COALESCE(SUM(CASE WHEN size_original > 0 AND size > size_original THEN size - size_original ELSE 0 END), 0) AS added").Scan(ctx, &agg)
			st.Processed, st.SpaceSaved = agg.Processed, agg.Saved
			st.SpaceAdded, st.NetSpaceSaved = agg.Added, agg.Saved-agg.Added
			s.processingActivity(ctx, &st)
			return &struct{ Body ProcessingStatus }{st}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "processing-history", Method: http.MethodGet, Path: "/api/v1/processing/history", Tags: tags,
		Summary: "Processed files per day: pages, size before and after, time spent"},
		func(ctx context.Context, in *struct {
			Days int `query:"days" default:"30" minimum:"1" maximum:"365"`
		}) (*struct{ Body []ProcessingDay }, error) {
			days, err := s.processingHistory(ctx, in.Days)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body []ProcessingDay }{days}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "processing-resume", Method: http.MethodPost, Path: "/api/v1/processing/resume", Tags: tags,
		Summary: "Resume re-encoding after it was paused because a library server couldn't read the files"},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			if err := s.app.Processing.Guard.Resume(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.PushProcessBacklog("resumed")
			return nil, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "processing-preview", Method: http.MethodPost, Path: "/api/v1/processing/preview", Tags: tags,
		Summary: "Process up to three chapter pages with the given settings to compare quality and size",
		Description: "Runs the same steps a download would: shrink wide pages, upscale narrow pages, split tall pages, then re-encode. " +
			"409 means no upscaler is available right now."},
		func(ctx context.Context, in *struct {
			Body struct {
				ChapterID int64                `json:"chapterId"`
				Encode    model.EncodeConfig   `json:"encode"`
				Upscale   *model.UpscaleConfig `json:"upscale,omitempty" doc:"Upscale settings to try first; omitted or disabled skips upscaling"`
				Pages     *model.PageRules     `json:"pages,omitempty" doc:"Page size rules (junk size, maximum width and tall-page splitting)"`
			}
		}) (*struct{ Body PreviewResult }, error) {
			upscale := in.Body.Upscale != nil && in.Body.Upscale.Enabled
			encoding := in.Body.Encode.Format != "" && in.Body.Encode.Format != "keep"
			var rules model.PageRules
			if in.Body.Pages != nil {
				rules = *in.Body.Pages
			}
			if !upscale && !encoding && rules.MaxWidth <= 0 && !rules.SplitTall {
				return nil, huma.Error422UnprocessableEntity("nothing to preview: every processing step is off")
			}
			if upscale && (s.app.Processing == nil || s.app.Processing.Up == nil) {
				return nil, huma.Error409Conflict("no upscaler available: upscaling is not set up on this server")
			}
			s.cleanPreviews()
			var f model.ChapterFile
			if err := s.app.DB.NewSelect().Model(&f).Where("chapter_id = ?", in.Body.ChapterID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("chapter has no file")
			}
			var ser model.Series
			if err := s.app.DB.NewSelect().Model(&ser).Where("id = ?", f.SeriesID).Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			dir, err := s.app.Library.SeriesDir(ctx, &ser)
			if err != nil {
				return nil, toHTTPError(err)
			}
			pages, _, err := cbz.Read(filepath.Join(dir, f.RelativePath))
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("read chapter: " + err.Error())
			}
			if len(pages) == 0 {
				return nil, huma.Error422UnprocessableEntity("chapter has no pages")
			}
			token := settings.RandomHex(8)
			work := filepath.Join(s.app.Cfg.DataDir, "preview", token)
			if err := os.MkdirAll(work, 0o775); err != nil {
				return nil, toHTTPError(err)
			}
			// first page after the cover, the middle one and a late one
			picks := []int{min(1, len(pages)-1), len(pages) / 2, max(len(pages)-2, 0)}
			var in2 []downloads.PageFile
			seen := map[int]bool{}
			for _, i := range picks {
				if seen[i] {
					continue
				}
				seen[i] = true
				info, err := imagecheck.Detect(pages[i].Data)
				if err != nil {
					continue
				}
				p := filepath.Join(work, pages[i].Name)
				if err := os.WriteFile(p, pages[i].Data, 0o664); err != nil {
					return nil, toHTTPError(err)
				}
				in2 = append(in2, downloads.PageFile{Name: pages[i].Name, Path: p, Format: info.Format, Width: info.Width, Height: info.Height})
			}
			res := PreviewResult{Token: token, Pages: []PreviewPage{}}
			// the same steps a download runs, minus the pause a library
			// server's failed read check puts on re-encoding
			proc := processing.New(nil, s.app.Encoder)
			if s.app.Processing != nil {
				cp := *s.app.Processing
				cp.Guard = nil
				proc = &cp
			}
			cfg := model.ProfileConfig{Encode: in.Body.Encode, Pages: rules}
			cfg.Encode.MinSavingsPct = -1000 // always show the encoded page
			if upscale {
				cfg.Upscale = *in.Body.Upscale
			}
			start := time.Now()
			pr, err := proc.Process(ctx, cfg, in2, work)
			if err != nil {
				os.RemoveAll(work)
				var none upscaling.ErrNoUpscaler
				if errors.As(err, &none) {
					return nil, huma.Error409Conflict(none.Error())
				}
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			out := pr.Pages
			res.Engine, res.Upscaler = pr.Encoder, pr.UpscaleModel
			res.UpscaleSeconds, res.EncodeSeconds = pr.UpscaleSeconds, pr.EncodeSeconds
			res.Seconds = time.Since(start).Seconds()
			pv := &preview{dir: work, created: time.Now()}
			counts := make(map[int]int, len(in2))
			for _, source := range pr.SourcePages {
				counts[source]++
			}
			for i := range out {
				source := i
				if i < len(pr.SourcePages) && pr.SourcePages[i] >= 0 && pr.SourcePages[i] < len(in2) {
					source = pr.SourcePages[i]
				}
				original := in2[source]
				pp := PreviewPage{Index: i, Name: out[i].Name, Width: original.Width, Height: original.Height, OriginalFormat: original.Format,
					EncodedFormat: out[i].Format, ResultWidth: out[i].Width, ResultHeight: out[i].Height, Upscaled: out[i].Width > original.Width,
					Shrunk: out[i].Width < original.Width, Split: counts[source] > 1, Junk: model.IsJunk(original.Width, original.Height, rules.JunkSize())}
				if fi, err := os.Stat(original.Path); err == nil {
					pp.OriginalSize = fi.Size()
				}
				if fi, err := os.Stat(out[i].Path); err == nil {
					pp.EncodedSize = fi.Size()
				}
				pv.orig, pv.enc = append(pv.orig, original.Path), append(pv.enc, out[i].Path)
				res.Pages = append(res.Pages, pp)
			}
			previewMu.Lock()
			previews[token] = pv
			previewMu.Unlock()
			return &struct{ Body PreviewResult }{res}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "processing-preview-image", Method: http.MethodGet, Path: "/api/v1/processing/preview/{token}/{index}/{variant}", Tags: tags},
		func(ctx context.Context, in *struct {
			Token   string `path:"token"`
			Index   int    `path:"index"`
			Variant string `path:"variant" enum:"original,encoded"`
		}) (*imageOutput, error) {
			previewMu.Lock()
			pv := previews[in.Token]
			previewMu.Unlock()
			if pv == nil || in.Index < 0 || in.Index >= len(pv.orig) {
				return nil, huma.Error404NotFound("preview expired")
			}
			p := pv.orig[in.Index]
			if in.Variant == "encoded" {
				p = pv.enc[in.Index]
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, huma.Error404NotFound("preview expired")
			}
			info, _ := imagecheck.Detect(data)
			return &imageOutput{ContentType: "image/" + info.Format, CacheControl: "private, max-age=3600", Body: data}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "processing-preview-cbz", Method: http.MethodGet, Path: "/api/v1/processing/preview/{token}/sample.cbz", Tags: tags,
		Summary: "The encoded preview pages as a CBZ, to check in a reader app"},
		func(ctx context.Context, in *struct {
			Token string `path:"token"`
		}) (*struct {
			ContentType        string `header:"Content-Type"`
			ContentDisposition string `header:"Content-Disposition"`
			Body               []byte
		}, error) {
			previewMu.Lock()
			pv := previews[in.Token]
			previewMu.Unlock()
			if pv == nil {
				return nil, huma.Error404NotFound("preview expired")
			}
			var pages []cbz.Page
			for i, p := range pv.enc {
				pages = append(pages, cbz.Page{Name: cbz.PageName(i, filepath.Ext(p)), Path: p})
			}
			dst := filepath.Join(pv.dir, "sample.cbz")
			if _, err := cbz.Write(dst, pages, nil, 0, time.Now()); err != nil {
				return nil, toHTTPError(err)
			}
			data, err := os.ReadFile(dst)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct {
				ContentType        string `header:"Content-Type"`
				ContentDisposition string `header:"Content-Disposition"`
				Body               []byte
			}{"application/vnd.comicbook+zip", fmt.Sprintf(`attachment; filename="mangarr-preview-%s.cbz"`, in.Token), data}, nil
		})
}
