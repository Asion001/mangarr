package reading

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Asion001/mangarr/internal/backupimport"
)

// MihonKomgaSourceID is the stable ID of the first Komga source instance in
// the Keiyoushi extension. Mihon uses this ID to reconnect restored entries.
const MihonKomgaSourceID = "4508733312114627536"

// MihonBackup exports the caller-visible library and reader's progress as a
// Mihon backup connected to mangarr's Komga-compatible API.
func (s *Service) MihonBackup(ctx context.Context, userID int64, address, apiKey string) ([]byte, error) {
	series, err := s.AllSeries(ctx, userID, 0)
	if err != nil {
		return nil, err
	}
	books, err := s.Books(ctx, userID, 0)
	if err != nil {
		return nil, err
	}
	bySeries := make(map[int64][]BookInfo)
	for _, book := range books {
		bySeries[book.Chapter.SeriesID] = append(bySeries[book.Chapter.SeriesID], book)
	}
	base := strings.TrimRight(address, "/")
	backup := &backupimport.Backup{Format: backupimport.FormatMihon, Sources: map[string]string{MihonKomgaSourceID: "Komga"}, Entries: []backupimport.BackupManga{}}
	for _, item := range series {
		ser := item.Series
		entry := backupimport.BackupManga{
			SourceID:     MihonKomgaSourceID,
			SourceName:   "Komga",
			URL:          fmt.Sprintf("%s/api/v1/series/%d", base, ser.ID),
			Title:        ser.Title,
			Author:       strings.Join(ser.Metadata.Authors, ", "),
			Artist:       strings.Join(ser.Metadata.Artists, ", "),
			Description:  ser.Metadata.Description,
			Genres:       append([]string(nil), ser.Metadata.Genres...),
			Status:       ser.Status,
			ThumbnailURL: fmt.Sprintf("%s/api/v1/series/%d/thumbnail", base, ser.ID),
			Favorite:     true,
			AddedAt:      ser.AddedAt,
			Trackers:     mihonTrackers(ser.Metadata.ExternalIDs),
			Chapters:     []backupimport.BackupChapter{},
		}
		for _, book := range bySeries[ser.ID] {
			chapter := book.Chapter
			name := strings.TrimSpace(chapter.Title)
			if name == "" {
				name = "Chapter " + chapter.NumberKey
			}
			out := backupimport.BackupChapter{
				URL:       fmt.Sprintf("%s/api/v1/books/%d", base, chapter.ID),
				Name:      name,
				Scanlator: book.Scanlator,
				Lang:      ser.Language,
				Number:    chapter.NumberSort,
			}
			if state := book.State; state != nil {
				out.Read = state.Completed
				if state.Page > 0 {
					out.LastPageRead = state.Page - 1 // Mihon stores a zero-based page index.
				}
				if state.ReadAt != nil {
					at := *state.ReadAt
					out.ReadAt = &at
				} else if state.Completed || state.Page > 0 {
					at := state.SyncedAt
					out.ReadAt = &at
				}
			}
			entry.Chapters = append(entry.Chapters, out)
		}
		backup.Entries = append(backup.Entries, entry)
	}
	return backupimport.MarshalMihon(backup, backupimport.MihonSourcePreferences{SourceID: MihonKomgaSourceID, Strings: map[string]string{
		"Address": base,
		"API key": apiKey,
	}}), nil
}

func mihonTrackers(ids map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{backupimport.TrackerAniList, backupimport.TrackerMAL, backupimport.TrackerKitsu, backupimport.TrackerMangaUpdates} {
		if id := strings.TrimSpace(ids[key]); id != "" {
			out[key] = id
		}
	}
	if out[backupimport.TrackerMAL] == "" {
		out[backupimport.TrackerMAL] = strings.TrimSpace(ids["myanimelist"])
	}
	for key, id := range out {
		if id == "" {
			delete(out, key)
		} else if _, err := strconv.ParseInt(id, 10, 64); err != nil && key != backupimport.TrackerMangaUpdates {
			delete(out, key)
		}
	}
	return out
}
