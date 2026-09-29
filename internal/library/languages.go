package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// AnyLanguage is the language of the automatic root folder: every language
// without a folder of its own gets <automatic folder>/<lang>.
const AnyLanguage = "*"

// NoFolderError means no root folder holds a language and there is no
// automatic folder to create one in.
type NoFolderError struct{ Language string }

func (e NoFolderError) Error() string {
	return fmt.Sprintf("no root folder for language %q: add one in Settings → Media management, or add an automatic folder", e.Language)
}

// NormalizeLanguage lower-cases a language code. Multi-language catalog codes
// ("all", "multi") are not an edition language and come back empty.
func NormalizeLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "all" || lang == "multi" {
		return ""
	}
	return lang
}

// Settings returns the settings store (for callers that need language defaults).
func (l *Library) Settings() *settings.Store { return l.settings }

// FolderForLanguage returns the root folder of a language. With an automatic
// folder it creates <automatic folder>/<lang> the first time; otherwise a
// language without a folder is a NoFolderError. When several folders share a
// language the oldest wins.
func (l *Library) FolderForLanguage(ctx context.Context, lang string) (*model.RootFolder, error) {
	path, exists, err := l.PlanFolder(ctx, lang)
	if err != nil {
		return nil, err
	}
	lang = NormalizeLanguage(lang)
	if exists {
		return l.findLanguageFolder(ctx, lang)
	}
	_, dmode := l.Modes(ctx)
	if err := os.MkdirAll(path, dmode); err != nil {
		return nil, fmt.Errorf("create the %s folder: %w", lang, err)
	}
	// a folder already registered at that path just gets the language
	var rf model.RootFolder
	if err := l.db.NewSelect().Model(&rf).Where("path = ?", path).Limit(1).Scan(ctx); err == nil {
		if rf.Language == "" {
			rf.Language = lang
			if _, err := l.db.NewUpdate().Model(&rf).Column("language").WherePK().Exec(ctx); err != nil {
				return nil, err
			}
		}
		return &rf, nil
	}
	rf = model.RootFolder{Path: path, Language: lang, CreatedAt: time.Now().UTC()}
	if _, err := l.db.NewInsert().Model(&rf).Exec(ctx); err != nil {
		// another add created it at the same moment
		if again, err2 := l.findLanguageFolder(ctx, lang); err2 == nil {
			return again, nil
		}
		return nil, fmt.Errorf("root folder %s: %w", path, err)
	}
	l.log.Info("created language folder", "language", lang, "path", path)
	return &rf, nil
}

// PlanFolder says where a language's titles go without creating anything:
// its folder, or the one FolderForLanguage would make (exists=false).
func (l *Library) PlanFolder(ctx context.Context, lang string) (path string, exists bool, err error) {
	lang = NormalizeLanguage(lang)
	if lang == "" || lang == AnyLanguage {
		return "", false, errors.New("pick a language for this edition")
	}
	if rf, err := l.findLanguageFolder(ctx, lang); err == nil {
		return rf.Path, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	auto, err := l.findLanguageFolder(ctx, AnyLanguage)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, NoFolderError{Language: lang}
	} else if err != nil {
		return "", false, err
	}
	return filepath.Join(auto.Path, lang), false, nil
}

func (l *Library) findLanguageFolder(ctx context.Context, lang string) (*model.RootFolder, error) {
	var rf model.RootFolder
	err := l.db.NewSelect().Model(&rf).Where("LOWER(language) = ?", lang).Order("id").Limit(1).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &rf, nil
}

// AssignFolderLanguages gives root folders without a language one: the
// language an old per-language default sent there, else the language most of
// its series use. Folders with a tie or no series stay empty. It runs at every
// start and only fills blanks.
func (l *Library) AssignFolderLanguages(ctx context.Context) error {
	var roots []model.RootFolder
	if err := l.db.NewSelect().Model(&roots).Where("language = ''").Order("id").Scan(ctx); err != nil || len(roots) == 0 {
		return err
	}
	fromDefaults := l.legacyDefaultFolders(ctx)
	for _, rf := range roots {
		lang := fromDefaults[rf.ID]
		if lang == "" {
			lang = l.majorityLanguage(ctx, rf.ID)
		}
		if lang == "" {
			continue
		}
		if _, err := l.db.NewUpdate().Model((*model.RootFolder)(nil)).Set("language = ?", lang).Where("id = ? AND language = ''", rf.ID).Exec(ctx); err != nil {
			return err
		}
		l.log.Info("root folder language set", "path", rf.Path, "language", lang)
	}
	return nil
}

// legacyDefaultFolders reads the root folder each language default pointed
// to before folders were chosen by language (settings saved before that still
// carry it). A folder two languages pointed to is ambiguous and skipped.
func (l *Library) legacyDefaultFolders(ctx context.Context) map[int64]string {
	var row model.Setting
	out := map[int64]string{}
	if err := l.db.NewSelect().Model(&row).Where("key = ?", settings.KeySources).Limit(1).Scan(ctx); err != nil {
		return out
	}
	var doc struct {
		LanguageDefaults []struct {
			Language     string `json:"language"`
			RootFolderID int64  `json:"rootFolderId"`
		} `json:"languageDefaults"`
	}
	if json.Unmarshal([]byte(row.Value), &doc) != nil {
		return out
	}
	ambiguous := map[int64]bool{}
	for _, d := range doc.LanguageDefaults {
		lang := NormalizeLanguage(d.Language)
		if d.RootFolderID == 0 || lang == "" {
			continue
		}
		if prev, ok := out[d.RootFolderID]; ok && prev != lang {
			ambiguous[d.RootFolderID] = true
		}
		out[d.RootFolderID] = lang
	}
	for id := range ambiguous {
		delete(out, id)
	}
	return out
}

func (l *Library) majorityLanguage(ctx context.Context, rootID int64) string {
	var rows []struct {
		Language string `bun:"language"`
		N        int    `bun:"n"`
	}
	err := l.db.NewSelect().Model((*model.Series)(nil)).ColumnExpr("LOWER(language) AS language, COUNT(*) AS n").
		Where("root_folder_id = ? AND preview = ?", rootID, false).GroupExpr("LOWER(language)").OrderExpr("n DESC").Scan(ctx, &rows)
	if err != nil || len(rows) == 0 || (len(rows) > 1 && rows[0].N == rows[1].N) {
		return ""
	}
	return NormalizeLanguage(rows[0].Language)
}
