package model

import (
	"github.com/uptrace/bun"
	"time"
)

// RecycledFile describes a whole archive or series folder retained on disk.
// Snapshots survive deletion of the original rows; nullable IDs are live links.
type RecycledFile struct {
	bun.BaseModel        `bun:"table:recycled_files"`
	ID                   int64           `bun:"id,pk,autoincrement" json:"id"`
	SeriesID             *int64          `bun:"series_id" json:"seriesId,omitempty"`
	ChapterID            *int64          `bun:"chapter_id" json:"chapterId,omitempty"`
	ChapterFileID        *int64          `bun:"chapter_file_id" json:"chapterFileId,omitempty"`
	Kind                 string          `bun:"kind,notnull" json:"kind"`
	Reason               string          `bun:"reason,notnull" json:"reason"`
	JobID                *int64          `bun:"job_id" json:"jobId,omitempty"`
	JobKind              string          `bun:"job_kind,notnull" json:"jobKind"`
	ProfileName          string          `bun:"profile_name,notnull" json:"profileName"`
	ProcessParams        string          `bun:"process_params,notnull" json:"processParams"`
	SeriesTitle          string          `bun:"series_title,notnull" json:"seriesTitle"`
	OriginalRelativePath string          `bun:"original_relative_path,notnull" json:"originalRelativePath"`
	RecycledPath         string          `bun:"recycled_path,notnull" json:"recycledPath"`
	Size                 int64           `bun:"size,notnull" json:"size"`
	PageCount            int             `bun:"page_count,notnull" json:"pageCount"`
	SHA256               string          `bun:"sha256,notnull" json:"sha256"`
	SourceName           string          `bun:"source_name,notnull" json:"sourceName"`
	Scanlator            string          `bun:"scanlator,notnull" json:"scanlator"`
	FileSnapshot         *ChapterFile    `bun:"file_snapshot,type:jsonb" json:"file,omitempty"`
	FolderSnapshot       *RecycledFolder `bun:"folder_snapshot,type:jsonb" json:"-"`
	RecycledAt           time.Time       `bun:"recycled_at,notnull" json:"recycledAt"`
}

type RecycledFolder struct {
	Series        Series           `json:"series"`
	Chapters      []Chapter        `json:"chapters"`
	Files         []ChapterFile    `json:"files"`
	ProcessParams map[int64]string `json:"processParams"`
}
