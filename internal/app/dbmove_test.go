package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbcopy"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
)

// TestMoveDatabase moves a running SQLite install to Postgres: writes wait
// meanwhile, the data arrives, the switch survives a restart (the DSN file
// config.Load reads) and mangarr asks to restart.
func TestMoveDatabase(t *testing.T) {
	dsns := dbtest.DSNs(t)
	pgDSN, ok := dsns["postgres"]
	if !ok {
		t.Skip("MANGARR_TEST_POSTGRES not set")
	}
	f := newKomgaFixture(t, dsns["sqlite"])
	a := f.e.App
	ctx := context.Background()

	if _, err := a.TestDatabase(ctx, a.Cfg.DB); err == nil {
		t.Fatal("moving to the current database must be refused")
	}
	info, err := a.TestDatabase(ctx, pgDSN)
	if err != nil || info.Kind != "postgres" || info.Rows != 0 {
		t.Fatalf("test: %+v %v", info, err)
	}

	srv := httptest.NewServer(api.New(a))
	defer srv.Close()
	g, _ := a.Settings.General(ctx)
	post := func(path, body string) int {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		req.Header.Set("X-Api-Key", g.APIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("/api/v1/system/database/move", `{"dsn":"`+pgDSN+`"}`); code != 204 && code != 200 {
		t.Fatalf("move: %d", code)
	}
	waitFor(t, 30*time.Second, "move done", func() bool { st := a.MoveState(); return st.Stage == "done" || st.Stage == "failed" })
	st := a.MoveState()
	if st.Stage != "done" || st.Result == nil || st.Result.Rows["chapters"] != 5 || !st.Restarting || strings.Contains(st.Target, "pg@") {
		t.Fatalf("state %+v", st)
	}
	// writes wait for the restart
	if !a.InMaintenance() {
		t.Fatal("not in maintenance")
	}
	if code := post("/api/v1/tags", `{"label":"late"}`); code != 503 {
		t.Fatalf("write during the move: %d", code)
	}
	select {
	case <-a.RestartRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("no restart requested")
	}

	// the data is in Postgres
	pg, err := db.Open(ctx, pgDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	var ser model.Series
	if err := pg.NewSelect().Model(&ser).Where("id = ?", f.ser.ID).Scan(ctx); err != nil || ser.Title != f.ser.Title || len(ser.Tags) != 1 {
		t.Fatalf("series in postgres: %+v %v", ser, err)
	}
	if n, _ := pg.NewSelect().Model((*model.ChapterReadState)(nil)).Count(ctx); n != 2 {
		t.Fatalf("read states: %d", n)
	}
	if n, _ := dbcopy.Rows(ctx, pg); n == 0 {
		t.Fatal("empty")
	}

	// the next start uses it
	b, err := os.ReadFile(filepath.Join(a.Cfg.DataDir, config.DBFileName))
	if err != nil || strings.TrimSpace(string(b)) != pgDSN {
		t.Fatalf("dsn file: %q %v", b, err)
	}
	t.Setenv("MANGARR_DATA_DIR", a.Cfg.DataDir)
	t.Setenv("MANGARR_DB", "")
	cfg, err := config.Load()
	if err != nil || cfg.DB != pgDSN || cfg.DBSource != "file" {
		t.Fatalf("config: %v %q %q", err, cfg.DB, cfg.DBSource)
	}
}

// TestPostgresBackupRestore: a Postgres install's backup holds the whole
// database and restores into a SQLite install.
func TestPostgresBackupRestore(t *testing.T) {
	dsns := dbtest.DSNs(t)
	if _, ok := dsns["postgres"]; !ok {
		t.Skip("MANGARR_TEST_POSTGRES not set")
	}
	ctx := context.Background()
	f := newKomgaFixture(t, dsns["postgres"])
	b, err := f.e.App.Backups.Create(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	path, _ := f.e.App.Backups.Path(b.Name)

	e := newTestApp(t, dsns["sqlite"])
	if err := e.App.RestoreBackup(path, b.Name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "restore done", func() bool { st := e.App.MoveState(); return st.Stage == "done" || st.Stage == "failed" })
	if st := e.App.MoveState(); st.Stage != "done" || st.Result.Rows["chapters"] != 5 {
		t.Fatalf("restore %+v", st)
	}
	var ser model.Series
	if err := e.App.DB.NewSelect().Model(&ser).Where("id = ?", f.ser.ID).Scan(ctx); err != nil || ser.Title != f.ser.Title {
		t.Fatalf("restored series %+v %v", ser, err)
	}
	if n, _ := e.App.DB.NewSelect().Model((*model.ReadingKey)(nil)).Count(ctx); n != 1 {
		t.Fatalf("reading keys %d", n)
	}
	select {
	case <-e.App.RestartRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("no restart after restoring")
	}
}

// TestSQLiteBackupRestoreIntoPostgres: the other direction, a SQLite
// install's backup restores into a Postgres install.
func TestSQLiteBackupRestoreIntoPostgres(t *testing.T) {
	dsns := dbtest.DSNs(t)
	if _, ok := dsns["postgres"]; !ok {
		t.Skip("MANGARR_TEST_POSTGRES not set")
	}
	ctx := context.Background()
	f := newKomgaFixture(t, dsns["sqlite"])
	b, err := f.e.App.Backups.Create(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	path, _ := f.e.App.Backups.Path(b.Name)

	e := newTestApp(t, dsns["postgres"])
	if err := e.App.RestoreBackup(path, b.Name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "restore done", func() bool { st := e.App.MoveState(); return st.Stage == "done" || st.Stage == "failed" })
	if st := e.App.MoveState(); st.Stage != "done" || st.Result.Rows["chapters"] != 5 {
		t.Fatalf("restore %+v", st)
	}
	var ser model.Series
	if err := e.App.DB.NewSelect().Model(&ser).Where("id = ?", f.ser.ID).Scan(ctx); err != nil || ser.Title != f.ser.Title {
		t.Fatalf("restored series %+v %v", ser, err)
	}
	if n, _ := e.App.DB.NewSelect().Model((*model.ReadingKey)(nil)).Count(ctx); n != 1 {
		t.Fatalf("reading keys %d", n)
	}
	select {
	case <-e.App.RestartRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("no restart after restoring")
	}
}
