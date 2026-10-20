package app

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbcopy"
)

// maintenance is on while the database moves: jobs hold and API writes
// answer 503, so nothing written in between is lost.
type maintenance struct {
	on      atomic.Bool
	restart chan struct{}
	once    sync.Once

	mu    sync.Mutex
	state MoveState
}

// InMaintenance reports whether the database is being moved.
func (a *App) InMaintenance() bool { return a.maint.on.Load() }

// RestartRequested is closed when the app should restart (after moving the
// database or on request); cmd/mangarr then re-executes itself.
func (a *App) RestartRequested() <-chan struct{} { return a.maint.restart }

// RequestRestart asks cmd/mangarr to restart the process.
func (a *App) RequestRestart() {
	a.maint.once.Do(func() {
		a.Log.Info("restarting")
		close(a.maint.restart)
	})
}

// MoveState is the progress of moving the database.
type MoveState struct {
	Running bool   `json:"running"`
	Stage   string `json:"stage,omitempty" enum:",preparing,snapshot,copying,switching,done,failed"`
	Table   string `json:"table,omitempty"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	// Target is the database the data goes to (password hidden).
	Target string         `json:"target,omitempty"`
	Error  string         `json:"error,omitempty"`
	Result *dbcopy.Result `json:"result,omitempty"`
	// SetEnv is set when MANGARR_DB pins the database: the data was copied,
	// and MANGARR_DB must be changed to Target before restarting.
	SetEnv     bool `json:"setEnv,omitempty"`
	Restarting bool `json:"restarting,omitempty"`
}

// MoveState returns the progress of the last database move.
func (a *App) MoveState() MoveState {
	a.maint.mu.Lock()
	defer a.maint.mu.Unlock()
	return a.maint.state
}

func (a *App) setMove(fn func(*MoveState)) {
	a.maint.mu.Lock()
	fn(&a.maint.state)
	a.maint.mu.Unlock()
	a.Bus.Changed("database", "move", 0)
}

// DBTest describes a database that data could move to.
type DBTest struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	// Rows is how much mangarr data it already holds (0 = empty).
	Rows int `json:"rows"`
}

// RedactDSN hides the password in a DSN.
func RedactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "<invalid>"
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			name := u.User.Username()
			u.User = url.User(name)
			return strings.Replace(u.String(), "//"+u.User.String()+"@", "//"+u.User.String()+":***@", 1)
		}
	}
	return u.String()
}

// TestDatabase connects to a database data could move to.
func (a *App) TestDatabase(ctx context.Context, dsn string) (*DBTest, error) {
	if err := a.checkTarget(dsn); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	d, err := db.Open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	t := &DBTest{Kind: string(d.Kind)}
	if d.Kind == db.Postgres {
		var num int
		if err := d.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&num); err != nil {
			return nil, err
		}
		t.Version = fmt.Sprintf("%d.%d", num/10000, num%10000)
		if num < 130000 {
			return t, fmt.Errorf("PostgreSQL %s is too old: 13 or newer is needed", t.Version)
		}
	} else {
		_ = d.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&t.Version)
	}
	// only a migrated database can hold mangarr data
	var exists bool
	if d.Kind == db.Postgres {
		_ = d.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'series')").Scan(&exists)
	} else {
		_ = d.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'series')").Scan(&exists)
	}
	if exists {
		if t.Rows, err = dbcopy.Rows(ctx, d); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (a *App) checkTarget(dsn string) error {
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") && !strings.HasPrefix(dsn, "sqlite://") {
		return errors.New("use a postgres:// or sqlite:// address")
	}
	if strings.TrimRight(dsn, "/") == strings.TrimRight(a.Cfg.DB, "/") {
		return errors.New("that's the database mangarr uses now")
	}
	return nil
}

// MoveDatabase copies all data to the database at dsn in the background
// and switches to it (restarting mangarr). Moving to the default SQLite
// file keeps the existing one next to it with a date in its name.
func (a *App) MoveDatabase(dsn string, overwrite bool) error {
	if err := a.checkTarget(dsn); err != nil {
		return err
	}
	a.maint.mu.Lock()
	if a.maint.state.Running {
		a.maint.mu.Unlock()
		return errors.New("the database is already being moved")
	}
	a.maint.state = MoveState{Running: true, Stage: "preparing", Target: RedactDSN(dsn)}
	a.maint.mu.Unlock()
	go a.moveDatabase(dsn, overwrite)
	return nil
}

func (a *App) moveDatabase(dsn string, overwrite bool) {
	ctx := context.Background()
	log := a.Log.With("component", "database", "target", RedactDSN(dsn))
	fail := func(err error) {
		log.Error("moving the database failed", "err", err)
		a.EndMaintenance()
		a.setMove(func(s *MoveState) { s.Running, s.Stage, s.Error = false, "failed", err.Error() })
	}
	log.Info("moving the database")
	a.enterMaintenance()

	defaultDB := config.DefaultDB(a.Cfg.DataDir)
	if dsn == defaultDB {
		// moving back to SQLite: keep the old file aside
		path := strings.TrimPrefix(defaultDB, "sqlite://")
		if _, err := os.Stat(path); err == nil {
			stamp := time.Now().Format("2006-01-02_15-04-05")
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if _, err := os.Stat(path + suffix); err == nil {
					if err := os.Rename(path+suffix, path+".before-move-"+stamp+suffix); err != nil {
						fail(err)
						return
					}
				}
			}
		}
	}
	dst, err := db.Open(ctx, dsn)
	if err != nil {
		fail(err)
		return
	}
	defer dst.Close()

	src := a.DB
	if a.DB.Kind == db.SQLite {
		a.setMove(func(s *MoveState) { s.Stage = "snapshot" })
		snap := filepath.Join(a.Cfg.DataDir, "move-snapshot.db")
		if err := a.DB.Backup(ctx, snap); err != nil {
			fail(fmt.Errorf("snapshot: %w", err))
			return
		}
		defer os.Remove(snap)
		if src, err = db.Open(ctx, "sqlite://"+snap); err != nil {
			fail(err)
			return
		}
		defer src.Close()
	}

	a.setMove(func(s *MoveState) { s.Stage = "copying" })
	res, err := dbcopy.Copy(ctx, src, dst, overwrite, a.copyProgress())
	if err != nil {
		fail(err)
		return
	}
	log.Info("database copied", "rows", res.Total, "duration", res.Duration.Round(time.Millisecond))

	a.setMove(func(s *MoveState) { s.Stage, s.Result = "switching", res })
	if a.Cfg.DBSource == "env" {
		// MANGARR_DB pins the database: the user changes it and restarts
		a.setMove(func(s *MoveState) { s.Running, s.Stage, s.SetEnv = false, "done", true })
		return
	}
	file := filepath.Join(a.Cfg.DataDir, config.DBFileName)
	if dsn == defaultDB {
		err = os.Remove(file)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	} else {
		err = os.WriteFile(file, []byte(dsn+"\n"), 0o600)
	}
	if err != nil {
		fail(err)
		return
	}
	a.setMove(func(s *MoveState) { s.Running, s.Stage, s.Restarting = false, "done", true })
	time.AfterFunc(1500*time.Millisecond, a.RequestRestart) // let the page see "done"
}

// enterMaintenance holds jobs and writes and waits (up to a minute) for
// running work to stop.
func (a *App) enterMaintenance() {
	a.maint.on.Store(true)
	a.Queue.Hold(true)
	a.Downloads.Hold(true)
	a.Downloads.CancelAll() // they're queued again after the restart
	deadline := time.Now().Add(time.Minute)
	for (a.Downloads.Running() > 0 || a.Queue.Running() > 0) && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
}

// copyProgress reports dbcopy's progress in MoveState.
func (a *App) copyProgress() dbcopy.Progress {
	var last time.Time
	return func(table string, done, total int) {
		a.maint.mu.Lock()
		a.maint.state.Table, a.maint.state.Done, a.maint.state.Total = table, done, total
		a.maint.mu.Unlock()
		if time.Since(last) > 250*time.Millisecond || done == total {
			last = time.Now()
			a.Bus.Changed("database", "move", 0)
		}
	}
}

// RestoreBackup replaces all data with the database in a backup zip (made
// by any mangarr install, on SQLite or Postgres) and restarts.
func (a *App) RestoreBackup(zipPath, name string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("not a mangarr backup: %w", err)
	}
	found := false
	for _, f := range zr.File {
		found = found || f.Name == "mangarr.db"
	}
	_ = zr.Close()
	if !found {
		return errors.New("this backup has no database in it (backups of Postgres installs made before this version only hold settings)")
	}
	a.maint.mu.Lock()
	if a.maint.state.Running {
		a.maint.mu.Unlock()
		return errors.New("the database is already being moved or restored")
	}
	a.maint.state = MoveState{Running: true, Stage: "preparing", Target: "backup " + name}
	a.maint.mu.Unlock()
	go a.restoreBackup(zipPath)
	return nil
}

func (a *App) restoreBackup(zipPath string) {
	ctx := context.Background()
	log := a.Log.With("component", "database", "backup", filepath.Base(zipPath))
	fail := func(err error) {
		log.Error("restoring the backup failed", "err", err)
		a.EndMaintenance()
		a.setMove(func(s *MoveState) { s.Running, s.Stage, s.Error = false, "failed", err.Error() })
	}
	log.Info("restoring a backup")
	a.enterMaintenance()
	tmp := filepath.Join(a.Cfg.DataDir, "restore.db")
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(tmp + suffix)
		}
	}()
	if err := extractFile(zipPath, "mangarr.db", tmp); err != nil {
		fail(err)
		return
	}
	src, err := db.Open(ctx, "sqlite://"+tmp)
	if err != nil {
		fail(err)
		return
	}
	defer src.Close()
	if err := src.Migrate(ctx); err != nil { // backups of older versions
		fail(fmt.Errorf("upgrade the backup: %w", err))
		return
	}
	a.setMove(func(s *MoveState) { s.Stage = "copying" })
	res, err := dbcopy.Copy(ctx, src, a.DB, true, a.copyProgress())
	if err != nil {
		fail(err)
		return
	}
	log.Info("backup restored", "rows", res.Total)
	a.setMove(func(s *MoveState) { s.Running, s.Stage, s.Result, s.Restarting = false, "done", res, true })
	time.AfterFunc(1500*time.Millisecond, a.RequestRestart)
}

func extractFile(zipPath, name, dst string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("%s not in the backup", name)
}

// EndMaintenance lets jobs and writes run again (after a failed move, or
// when mangarr must be restarted by hand).
func (a *App) EndMaintenance() {
	a.maint.on.Store(false)
	a.Queue.Hold(false)
	a.Downloads.Hold(false)
}
