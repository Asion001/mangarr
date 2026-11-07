package envcfg

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
)

// ManagedEnv marks rows created from environment variables.
const ManagedEnv = "env"

type rootFolder struct{ path, lang string }

func parseRootFolders(v string) ([]rootFolder, error) {
	var out []rootFolder
	for _, part := range splitList(v) {
		p, lang, _ := strings.Cut(part, "|")
		p = filepath.Clean(strings.TrimSpace(p))
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("%s: %q is not an absolute path", RootFoldersVar, p)
		}
		out = append(out, rootFolder{p, strings.TrimSpace(lang)})
	}
	return out, nil
}

// SyncRootFolders creates the root folders listed in MANGARR_ROOT_FOLDERS and
// marks them managed. Folders removed from the variable become editable again.
func SyncRootFolders(ctx context.Context, d *db.DB, env map[string]string) error {
	v, set := env[RootFoldersVar]
	var folders []rootFolder
	if set {
		var err error
		if folders, err = parseRootFolders(v); err != nil {
			return err
		}
	}
	return d.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		keep := []string{}
		for _, f := range folders {
			var rf model.RootFolder
			err := tx.NewSelect().Model(&rf).Where("path = ?", f.path).Limit(1).Scan(ctx)
			if err != nil {
				rf = model.RootFolder{Path: f.path, Language: f.lang, ManagedBy: ManagedEnv, CreatedAt: time.Now().UTC()}
				if _, err := tx.NewInsert().Model(&rf).Exec(ctx); err != nil {
					return fmt.Errorf("root folder %s: %w", f.path, err)
				}
			} else {
				q := tx.NewUpdate().Model(&rf).Set("managed_by = ?", ManagedEnv).WherePK()
				if f.lang != "" {
					q = q.Set("language = ?", f.lang)
				}
				if _, err := q.Exec(ctx); err != nil {
					return err
				}
			}
			keep = append(keep, f.path)
		}
		q := tx.NewUpdate().Model((*model.RootFolder)(nil)).Set("managed_by = ''").Where("managed_by = ?", ManagedEnv)
		if len(keep) > 0 {
			q = q.Where("path NOT IN (?)", bun.List(keep))
		}
		_, err := q.Exec(ctx)
		return err
	})
}
