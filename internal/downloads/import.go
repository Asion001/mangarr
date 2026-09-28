package downloads

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/comicinfo"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
)

// importChapter writes the processed pages to the library, commits file/job
// state and history, then publishes events. Its caller handles any failure.
func (m *Manager) importChapter(ctx context.Context, jc *jobCtx, proc ProcessResult, params string, sizeBefore int64) error {
	defer library.RLockSeries(jc.series.ID)() // a move/rename waits for the import
	// the series may have moved while pages were downloading
	var loc model.Series
	if err := m.db.NewSelect().Model(&loc).Column("root_folder_id", "path").Where("id = ?", jc.series.ID).Scan(ctx); err == nil {
		jc.series.RootFolderID, jc.series.Path = loc.RootFolderID, loc.Path
	}
	if jc.file != nil { // and its file may have been renamed
		var f model.ChapterFile
		if err := m.db.NewSelect().Model(&f).Column("relative_path").Where("id = ?", jc.file.ID).Scan(ctx); err == nil {
			jc.file.RelativePath = f.RelativePath
		}
	}
	pages, upscaled, upscaleModel := proc.Pages, proc.Upscaled, proc.UpscaleModel
	mm, _ := m.settings.MediaManagement(ctx)
	dir, err := m.lib.EnsureSeriesDir(ctx, &jc.series)
	if err != nil {
		return infraError{err}
	}
	if mm.MinFreeSpaceMB > 0 {
		if free, err := fsutil.FreeSpace(dir); err == nil && free < uint64(mm.MinFreeSpaceMB)<<20 {
			return infraError{fmt.Errorf("not enough free space in %s (%d MB free, %d MB required)", dir, free>>20, mm.MinFreeSpaceMB)}
		}
	}
	// Keep the existing path on upgrades/re-processing so reader servers keep
	// book ids and read progress.
	rel := ""
	if jc.file != nil {
		rel = jc.file.RelativePath
	} else {
		sourceName := ""
		if jc.link != nil {
			sourceName = jc.link.SourceName
		}
		rel = m.lib.ChapterFileName(ctx, &jc.series, &jc.chapter, jc.release, sourceName)
	}
	target := filepath.Join(dir, rel)
	// re-encoding to save space may skip the recycle bin (the whole point is
	// to free the space); everything else keeps the replaced file around
	recycle := !(jc.job.Kind == model.JobKindReprocess && proc.Encoded > 0 && !jc.profile.Config.Encode.RecycleOriginals)
	if _, err := os.Stat(target); err == nil && recycle {
		reason := "upgraded"
		if jc.job.Kind == model.JobKindReprocess {
			reason = "reprocessed"
		}
		if _, err := m.lib.Recycle(ctx, target, jc.series.Path, true, library.RecycleInfo{Series: &jc.series, File: jc.file, Reason: reason, Job: jc.job, Profile: &jc.profile}); err != nil {
			return infraError{err}
		}
	}

	ci := m.comicInfo(ctx, jc, len(pages), mm.WriteVolume)
	xmlData, err := ci.Marshal()
	if err != nil {
		return err
	}
	fmode, _ := m.lib.Modes(ctx)
	cbzPages := make([]cbz.Page, len(pages))
	widthSum := 0
	formats := map[string]int{}
	for i, p := range pages {
		cbzPages[i] = cbz.Page{Name: p.Name, Path: p.Path}
		widthSum += p.Width
		formats[p.Format]++
	}
	res, err := cbz.Write(target, cbzPages, xmlData, fmode, time.Now())
	if err != nil {
		return infraError{fmt.Errorf("write %s: %w", target, err)}
	}
	format := ""
	for f, n := range formats {
		if n > formats[format] {
			format = f
		}
	}
	now := time.Now().UTC()
	file := &model.ChapterFile{ChapterID: jc.chapter.ID, SeriesID: jc.series.ID, RelativePath: rel, Size: res.Size, PageCount: len(pages),
		Format: format, SHA256: res.SHA256, Upscaled: upscaled, UpscaleModel: upscaleModel, ImportedAt: now}
	if len(pages) > 0 {
		file.AvgWidth = widthSum / len(pages)
	}
	if upscaled {
		file.SizeBefore = sizeBefore
	}
	file.SizeOriginal = res.Size
	if proc.Changed {
		file.SizeOriginal = sizeBefore // pages as downloaded
	}
	if jc.job.Kind == model.JobKindReprocess && jc.inputFile != nil {
		file.SizeOriginal = jc.inputFile.SizeOriginal
		if file.SizeOriginal == 0 {
			file.SizeOriginal = jc.inputFile.Size
		}
	}
	if params != "" {
		file.ProcessParams, file.ProcessState, file.ProcessedAt = params, model.ProcessDone, &now
		file.ProcessSeconds, file.ProcessPages = proc.Seconds, proc.ProcessedPages
	}
	if jc.release != nil {
		file.ReleaseID, file.Scanlator = &jc.release.ID, jc.release.Scanlator
	}
	if jc.link != nil {
		file.SourceName = jc.link.SourceName
	}
	if jc.job.Kind == model.JobKindReprocess && jc.inputFile != nil {
		file.ReleaseID, file.Scanlator, file.SourceName = jc.inputFile.ReleaseID, jc.inputFile.Scanlator, jc.inputFile.SourceName
		if !upscaled {
			file.Upscaled, file.UpscaleModel = jc.inputFile.Upscaled, jc.inputFile.UpscaleModel
		}
	}
	file.SourcePages = append([]int(nil), proc.SourcePages...)
	if jc.inputFile != nil && len(jc.inputFile.SourcePages) > 0 {
		if len(file.SourcePages) == 0 {
			file.SourcePages = append([]int(nil), jc.inputFile.SourcePages...)
		} else {
			for i, source := range file.SourcePages {
				if source < len(jc.inputFile.SourcePages) {
					file.SourcePages[i] = jc.inputFile.SourcePages[source]
				}
			}
		}
	}
	// Keep the row identity when replacing a chapter: reader servers key
	// book IDs and progress on it.
	if jc.file != nil && jc.file.RelativePath == rel {
		file.ID, file.ImportedAt = jc.file.ID, jc.file.ImportedAt
	}
	event := model.HistoryImported
	if jc.file != nil {
		event = model.HistoryUpgraded
		if jc.job.Kind == model.JobKindReprocess {
			event = model.HistoryProcessed
		}
	}
	err = m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if file.ID != 0 {
			if _, err := tx.NewUpdate().Model(file).WherePK().Exec(ctx); err != nil {
				return err
			}
		} else {
			if _, err := tx.NewDelete().Model((*model.ChapterFile)(nil)).Where("chapter_id = ?", jc.chapter.ID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewInsert().Model(file).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewUpdate().Model((*model.Chapter)(nil)).Set("file_id = ?", file.ID).Set("state = ?", model.ChapterImported).
			Set("cleaned_at = NULL").Set("updated_at = ?", now).Where("id = ?", jc.chapter.ID).Exec(ctx); err != nil {
			return err
		}
		if jc.file != nil {
			if err := library.RemapProgress(ctx, tx, jc.chapter.ID, jc.file, file); err != nil {
				return err
			}
		}
		jc.job.Status, jc.job.Progress, jc.job.Error, jc.job.UpdatedAt = model.JobCompleted, 100, "", now
		if _, err := tx.NewUpdate().Model(jc.job).Column("status", "progress", "error", "updated_at").WherePK().Exec(ctx); err != nil {
			return err
		}
		src := ""
		if jc.release != nil {
			src = jc.release.Name
		}
		chID := jc.chapter.ID
		return history.Record(ctx, tx, jc.series.ID, &chID, event, src, map[string]string{
			"path": rel, "size": strconv.FormatInt(res.Size, 10), "pages": strconv.Itoa(len(pages)),
			"source": file.SourceName, "scanlator": file.Scanlator, "upscaled": strconv.FormatBool(upscaled),
			"encoded": strconv.Itoa(proc.Encoded), "sizeOriginal": strconv.FormatInt(file.SizeOriginal, 10)})
	})
	if err != nil {
		return err
	}
	evType := events.ChapterImported
	if event == model.HistoryUpgraded {
		evType = events.ChapterUpgraded
	}
	if event != model.HistoryUpscaled && event != model.HistoryProcessed {
		m.bus.Publish(events.Event{Type: evType, SeriesID: jc.series.ID, Payload: events.ChapterImportedPayload{
			SeriesTitle: jc.series.Title, Chapter: jc.chapter.NumberKey, NumberSort: jc.chapter.NumberSort, Title: jc.chapter.Title,
			Source: file.SourceName, Scanlator: file.Scanlator, Upgrade: event == model.HistoryUpgraded, Upscaled: upscaled,
			CoverURL: jc.series.Metadata.CoverURL}})
	}
	m.bus.Publish(events.Event{Type: EventFileWritten, SeriesID: jc.series.ID, Payload: target})
	if proc.Encoded > 0 {
		m.bus.Publish(events.Event{Type: EventFileEncoded, SeriesID: jc.series.ID, Payload: EncodedPayload{Path: target, Format: file.Format}})
	}
	m.bus.Changed("chapter", "updated", jc.chapter.ID)
	m.bus.Changed("series", "updated", jc.series.ID)
	m.bus.Changed("queue", "updated", jc.job.ID)
	return nil
}

// remapPage keeps an in-progress reader on the first segment of the same
// original page. Completed progress points at the new final page.
func remapPage(page int, completed bool, sourcePages []int) int {
	if page <= 0 || len(sourcePages) == 0 {
		return page
	}
	if completed {
		return len(sourcePages)
	}
	source := page - 1
	for i, original := range sourcePages {
		if original >= source {
			return i + 1
		}
	}
	return len(sourcePages)
}

// EventFileWritten is published (payload: absolute path) whenever a library
// file is written or deleted, so library modules can rescan.
const EventFileWritten = "library.file"

// EventFileEncoded is published when pages were re-encoded (payload EncodedPayload).
const EventFileEncoded = "library.encoded"

type EncodedPayload struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

func (m *Manager) comicInfo(ctx context.Context, jc *jobCtx, pageCount int, writeVolume bool) comicinfo.ComicInfo {
	s := jc.series
	in := comicinfo.Input{
		SeriesTitle: s.Title, ChapterNumberKey: jc.chapter.NumberKey, ChapterTitle: jc.chapter.Title,
		Summary: s.Metadata.Description, Authors: s.Metadata.Authors, Artists: s.Metadata.Artists, Publisher: s.Metadata.Publisher,
		Genres: s.Metadata.Genres, Tags: s.Metadata.Tags, PageCount: pageCount, Language: s.Language,
		ReadingDirection: s.ReadingDirection, AgeRating: s.Metadata.AgeRating, ReleaseDate: jc.chapter.ReleaseDate,
	}
	if writeVolume {
		in.Volume = jc.chapter.Volume
	}
	if jc.release != nil {
		in.Scanlator = jc.release.Scanlator
		if jc.release.UploadDate != nil {
			in.ReleaseDate = jc.release.UploadDate
		}
		in.WebLinks = append(in.WebLinks, jc.release.WebURL)
	} else if jc.file != nil {
		in.Scanlator = jc.file.Scanlator
	}
	if u := s.Metadata.Links["AniList"]; u != "" {
		in.WebLinks = append(in.WebLinks, u)
	}
	if s.Status == model.StatusCompleted || s.Status == model.StatusCancelled {
		in.TotalCount = s.Metadata.TotalChapters
		if in.TotalCount == 0 {
			n, _ := m.db.NewSelect().Model((*model.Chapter)(nil)).Where("series_id = ?", s.ID).Count(ctx)
			in.TotalCount = n
		}
	}
	if jc.link != nil {
		in.Notes = "Downloaded by mangarr from " + jc.link.SourceName
	}
	return comicinfo.Build(in)
}
