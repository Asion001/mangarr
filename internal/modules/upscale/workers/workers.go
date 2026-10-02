// Package workers upscales through mangarr's own workers: a batch of pages
// becomes a task, a worker with the upscale role takes it, and the result
// comes back the same way. It is an upscale module like any other, so the
// profile, the chunking and the progress reporting are unchanged — only the
// machine that runs the engine is somewhere else.
package workers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/upscale"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// Settings are what an admin can change about this module.
type Settings struct {
	// TimeoutMinutes is how long a batch may take on a worker before the
	// server does it itself.
	TimeoutMinutes int `json:"timeoutMinutes" label:"Give up after (minutes)" order:"1" advanced:"true"`
}

func init() {
	modules.Register(&modules.Implementation{
		Kind: modules.KindUpscale, Name: "workers", DisplayName: "mangarr workers",
		Description: "Upscales on the machines that have the upscale role in System → Workers.",
		Settings:    func() any { return &Settings{TimeoutMinutes: 30} },
		New: func(d modules.Deps, s any) (modules.Instance, error) {
			cfg := s.(*Settings)
			return &Module{log: d.Log, timeout: time.Duration(max(cfg.TimeoutMinutes, 1)) * time.Minute}, nil
		},
	})
}

// Module hands upscaling to the workers.
type Module struct {
	log     *slog.Logger
	timeout time.Duration
}

// ErrNoWorker means no worker with the upscale role is around.
var ErrNoWorker = errors.New("no upscaling worker is online")

func (m *Module) Test(ctx context.Context) error {
	info, err := m.Info(ctx)
	if err != nil {
		return err
	}
	if len(info.Models) == 0 {
		return errors.New("the workers that are online have no upscaling models")
	}
	return nil
}

// HealthCheck stays quiet when every upscale worker is intentionally
// disabled. Once at least one is enabled, Test reports whether it is online
// and has an upscaling model as before.
func (m *Module) HealthCheck(ctx context.Context) (string, error) {
	list, err := configured(ctx)
	if err != nil {
		return "", err
	}
	for _, w := range list {
		if w.Enabled {
			return "", m.Test(ctx)
		}
	}
	if len(list) > 0 {
		return "", nil // every configured upscale worker was switched off
	}
	return "", m.Test(ctx)
}

// Info merges what the online workers said they can do when they last
// dialled in.
func (m *Module) Info(ctx context.Context) (*upscale.Info, error) {
	list, err := online(ctx)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNoWorker
	}
	info := &upscale.Info{Models: []upscale.Model{}}
	seen := map[string]bool{}
	for _, w := range list {
		if info.Version == "" {
			info.Version = w.Version
		}
		for _, d := range stringList(w.Info["devices"]) {
			info.Devices = append(info.Devices, w.Name+": "+d)
		}
		for _, mdl := range models(w.Info["models"]) {
			if seen[mdl.Name] {
				continue
			}
			seen[mdl.Name] = true
			info.Models = append(info.Models, mdl)
		}
	}
	return info, nil
}

// Upscale sends a batch to a worker and waits for it to come back.
func (m *Module) Upscale(ctx context.Context, images []upscale.Image, p upscale.Params) ([]upscale.Image, error) {
	tasks, err := ready(ctx)
	if err != nil {
		return nil, err
	}
	jobID := worktasks.JobFrom(ctx)
	if jobID == 0 {
		return nil, errors.New("upscaling on a worker only happens as part of a download job")
	}
	dir := filepath.Join(tasks.DataDir, "staging", "upscale")
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return nil, err
	}
	in, err := os.CreateTemp(dir, "batch-*.zip")
	if err != nil {
		return nil, err
	}
	inPath := in.Name()
	outPath := inPath + ".out"
	defer func() {
		_ = os.Remove(inPath)
		_ = os.Remove(outPath)
		_ = os.Remove(outPath + ".part")
		parts, _ := filepath.Glob(outPath + ".part.*")
		for _, part := range parts {
			_ = os.Remove(part)
		}
	}()
	if err := writeZip(in, images); err != nil {
		in.Close()
		return nil, err
	}
	if err := in.Close(); err != nil {
		return nil, err
	}

	params, _ := json.Marshal(p)
	spec := map[string]any{"params": json.RawMessage(params), "input": inPath, "output": outPath, "pages": len(images)}
	if pin, ok := worktasks.PinFrom(ctx); ok {
		pin.Apply(spec) // an upscale route's worker
	}
	task := &model.WorkerTask{JobID: jobID, Kind: model.TaskUpscale, Spec: spec, PagesTotal: len(images)}
	if err := tasks.Add(ctx, task); err != nil {
		return nil, err
	}
	done := tasks.Await(task.ID)
	defer tasks.Forget(task.ID)

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
	case <-time.After(m.timeout):
		_ = tasks.CancelTask(ctx, task.ID)
		return nil, fmt.Errorf("no worker finished this batch in %s", m.timeout)
	case <-ctx.Done():
		_ = tasks.CancelTask(ctx, task.ID)
		return nil, ctx.Err()
	}
	out, err := readZip(outPath)
	if err != nil {
		return nil, fmt.Errorf("the worker's result: %w", err)
	}
	if len(out) != len(images) {
		return nil, fmt.Errorf("the worker returned %d of %d pages", len(out), len(images))
	}
	if used := tasks.UsedModel(ctx, task.ID); used != "" {
		upscale.ReportUsed(ctx, used)
	} else {
		upscale.ReportUsed(ctx, p.Model)
	}
	return out, nil
}

// Rank places this module where its best online worker stands, so the
// workers and this server share one priority list.
func (m *Module) Rank(ctx context.Context) (int, bool) {
	list, err := online(ctx)
	if err != nil {
		return 0, false
	}
	best, found := 0, false
	for _, w := range list {
		if len(models(w.Info["models"])) == 0 {
			continue
		}
		if !found || w.Priority < best {
			best, found = w.Priority, true
		}
	}
	return best, found
}

// ready is the ledger this process hands work to, once there is a worker
// that could take it.
func ready(ctx context.Context) (*worktasks.Ledger, error) {
	l := worktasks.Default()
	if l == nil {
		return nil, ErrNoWorker
	}
	if list, err := online(ctx); err != nil || len(list) == 0 {
		return nil, ErrNoWorker
	}
	return l, nil
}

// online is the enabled workers with the upscale role that have been here
// recently.
func online(ctx context.Context) ([]model.Worker, error) {
	list, err := enabled(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Worker, 0, len(list))
	for _, w := range list {
		if w.LastSeenAt != nil && time.Since(*w.LastSeenAt) < worktasks.OnlineWithin {
			out = append(out, w)
		}
	}
	return out, nil
}

// enabled is the set of workers an admin currently expects to upscale.
func enabled(ctx context.Context) ([]model.Worker, error) {
	list, err := configured(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Worker, 0, len(list))
	for _, w := range list {
		if w.Enabled {
			out = append(out, w)
		}
	}
	return out, nil
}

func configured(ctx context.Context) ([]model.Worker, error) {
	l := worktasks.Default()
	if l == nil {
		return nil, ErrNoWorker
	}
	d := l.DB()
	var list []model.Worker
	if err := d.NewSelect().Model(&list).Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]model.Worker, 0, len(list))
	for _, w := range list {
		if w.HasRole(model.RoleUpscale) {
			out = append(out, w)
		}
	}
	return out, nil
}

// ---- the batch on the wire ---------------------------------------------------

func writeZip(w io.Writer, images []upscale.Image) error {
	zw := zip.NewWriter(w)
	for _, img := range images {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: img.Name, Method: zip.Store})
		if err != nil {
			return err
		}
		if _, err := f.Write(img.Data); err != nil {
			return err
		}
	}
	return zw.Close()
}

func readZip(path string) ([]upscale.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := make([]upscale.Image, 0, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, upscale.Image{Name: filepath.Base(f.Name), Data: b})
	}
	return out, nil
}

// ---- reading what a worker said about itself ---------------------------------

func stringList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func models(v any) []upscale.Model {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []upscale.Model
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

var _ upscale.Module = (*Module)(nil)
var _ modules.HealthChecker = (*Module)(nil)
var _ upscale.Ranked = (*Module)(nil)
