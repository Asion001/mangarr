package upscaler

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A batch's work folder is removed when the batch ends, but a restart in the
// middle of one leaves it behind, and on a busy server those add up to
// hundreds of gigabytes. The server sweeps them on start and then at most
// once per sweepEvery, when a batch begins.
const (
	tmpPrefix  = "upscale-"
	sweepEvery = time.Hour
	// staleAfter is how long a work folder sits untouched before it counts as
	// left behind; another server may share the folder, and its batches can
	// wait for a GPU a long while.
	staleAfter = 6 * time.Hour
)

// maybeSweep removes left-behind work folders unless it ran recently.
func (s *Server) maybeSweep() {
	now := time.Now()
	last := s.lastSweep.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < sweepEvery {
		return
	}
	if !s.lastSweep.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	go s.sweepTmp(s.cfg.TmpDir, now)
}

// sweepTmp removes the work folders in tmp that no batch of this server is
// using and that nothing has written to for staleAfter.
func (s *Server) sweepTmp(tmp string, now time.Time) {
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	stale := max(staleAfter, 2*s.cfg.Timeout)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), tmpPrefix) {
			continue
		}
		dir := filepath.Join(tmp, e.Name())
		if _, busy := s.active.Load(dir); busy || now.Sub(lastWrite(dir)) < stale {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			s.log.Warn("upscaler: removing a left-behind work folder failed", "dir", dir, "error", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		s.log.Info("upscaler: removed left-behind work folders", "count", removed, "dir", tmp)
	}
}

// lastWrite is the newest change to a work folder or its in/out folders,
// which the engine writes to as it goes.
func lastWrite(dir string) time.Time {
	var t time.Time
	for _, p := range []string{dir, filepath.Join(dir, "in"), filepath.Join(dir, "out")} {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}
