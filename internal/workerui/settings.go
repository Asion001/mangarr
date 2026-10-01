// Package workerui is the desktop face of mangarr-worker: a small page on
// 127.0.0.1 where the worker is set up, started and stopped, and where its
// progress, totals and log can be watched. It is plain HTML served by the
// worker itself, so the same exe works on Windows, macOS and Linux with
// nothing else installed.
package workerui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Fields are the variables the page edits, in the order it shows them.
// Anything else (prefetch, tile size...) can still be set in the
// environment.
var Fields = []string{
	"MANGARR_SERVER_URL",
	"MANGARR_WORKER_KEY",
	"MANGARR_WORKER_ROLES",
	"MANGARR_WORKER_CONCURRENT",
	"MANGARR_UPSCALER_TOOLS_DIR",
	"MANGARR_UPSCALER_GPU",
	"MANGARR_UPSCALER_TILE",
	KeepAwakeVar,
	"MANGARR_WORKER_AUTO_UPDATE",
}

// Store keeps the page's settings in a JSON file, as the same variables
// the worker reads from the environment. The environment wins: a variable
// set there is shown on the page but can't be changed from it.
type Store struct {
	path     string
	env      func(string) string
	defaults map[string]string

	mu   sync.Mutex
	vals map[string]string
}

// DefaultPath is where the settings live: the user's config folder
// (%AppData% on Windows), or MANGARR_WORKER_SETTINGS.
func DefaultPath() string {
	if p := os.Getenv("MANGARR_WORKER_SETTINGS"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "mangarr-worker", "settings.json")
}

// Open reads the settings at path (a missing file is an empty one).
// defaults fill what neither the file nor the environment sets.
func Open(path string, env func(string) string, defaults map[string]string) (*Store, error) {
	s := &Store{path: path, env: env, defaults: defaults, vals: map[string]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.vals); err != nil {
		return nil, err
	}
	return s, nil
}

// Getenv is what the worker reads its configuration with.
func (s *Store) Getenv(name string) string {
	if v := s.env(name); v != "" {
		return v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.vals[name]; v != "" {
		return v
	}
	return s.defaults[name]
}

// Locked reports whether the environment sets name.
func (s *Store) Locked(name string) bool { return s.env(name) != "" }

// Saved is the value the file holds for name.
func (s *Store) Saved(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vals[name]
}

// Save replaces the given values (an empty one removes it) and writes the
// file, readable by this user only: it holds the worker's key.
func (s *Store) Save(vals map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[string]string{}
	for k, v := range s.vals {
		next[k] = v
	}
	for k, v := range vals {
		if v == "" {
			delete(next, k)
		} else {
			next[k] = v
		}
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.vals = next
	return nil
}

// Path is the settings file.
func (s *Store) Path() string { return s.path }
