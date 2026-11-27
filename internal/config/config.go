// Package config loads process-level configuration from environment variables.
// Runtime-editable settings live in the database (see internal/settings).
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Asion001/mangarr/internal/clientip"
	"github.com/Asion001/mangarr/internal/libfs"
)

type Config struct {
	// Mode: integrated (server + built-in upscaler when the image has the
	// tools), server (no local upscaling) or worker (does work for a server).
	Mode string
	// Listen address, e.g. ":8787".
	Listen string
	// DataDir holds the SQLite database, staging area, backups, logs and caches.
	DataDir string
	// DB is a DSN: "sqlite:///config/mangarr.db" (default) or "postgres://user:pass@host:5432/db".
	DB string
	// DBSource says where DB came from: "env" (MANGARR_DB), "file"
	// (DBFileName in the data dir, written when the database was moved in
	// the UI) or "default" (SQLite in the data dir).
	DBSource string
	// LogLevel: debug, info, warn, error.
	LogLevel string
	// LogDir holds rotating log files ("" = no log files).
	LogDir string
	// LogFiles is how many log files are kept (current plus rotated).
	LogFiles int
	// AuthDisabled turns off login + API key checks (for use behind an auth proxy).
	AuthDisabled bool
	// URLBase allows serving under a sub path, e.g. "/mangarr".
	URLBase string
	// WebDir overrides the embedded UI with files from disk (development).
	WebDir string
	// KomgaListen is where the Komga-compatible API listens while enabled.
	KomgaListen string
	// Processing says where downloaded pages are processed (resized, split,
	// upscaled, re-encoded): ProcessingLocal in this process, or
	// ProcessingWorkers on a worker with the encode role.
	Processing string
	// Pprof serves Go's profiler to admins at /api/v1/system/pprof/.
	Pprof bool
	// TrustedProxies are the reverse proxies whose forwarding headers
	// (X-Forwarded-For, -Proto, -Host) are believed. Nil: clientip.DefaultTrusted.
	TrustedProxies []netip.Prefix
	// FrameAncestors may show the UI in a frame (CSP frame-ancestors;
	// "" = this site only).
	FrameAncestors string
	// Storage is the object storage that holds the library; nil keeps it on
	// the local disk.
	Storage *libfs.S3Config
	// Env is the MANGARR_* environment used to pin settings, root folders and
	// modules (see internal/envcfg). Nil in tests unless set explicitly.
	Env map[string]string
}

// VarDoc documents a process-level variable.
type VarDoc struct{ Name, Default, Description string }

// Vars lists the process-level variables read by Load.
var Vars = []VarDoc{
	{"MANGARR_MODE", "integrated", "integrated (server, plus a built-in upscaler when the image has the tools), server, or worker (asks a server for work; upscaler is a deprecated alias)."},
	{"MANGARR_LISTEN", ":8787", "HTTP listen address."},
	{"MANGARR_DATA_DIR", "./config (/config in Docker)", "Database, staging, backups, recycle bin and caches."},
	{"MANGARR_DB", "sqlite://$MANGARR_DATA_DIR/mangarr.db", "Database DSN: sqlite://… or postgres://user:pass@host:5432/db."},
	{"MANGARR_LOG_LEVEL", "info", "debug, info, warn or error."},
	{"MANGARR_LOG_DIR", "$MANGARR_DATA_DIR/logs", "Folder for log files (rotated at 5 MB); off disables them."},
	{"MANGARR_LOG_FILES", "5", "How many log files to keep."},
	{"MANGARR_URL_BASE", "", "Serve under a sub path, e.g. /mangarr."},
	{"MANGARR_AUTH_DISABLED", "false", "Disable login and API key checks (only behind an auth proxy)."},
	{"MANGARR_WEB_DIR", "", "Serve the UI from this directory instead of the embedded copy (development)."},
	{"MANGARR_PROCESSING", "local", "Where downloaded pages are processed (resized, split, upscaled, re-encoded): local (in this process) or workers (on a worker with the encode role, so heavy image work never runs in the server)."},
	{"MANGARR_PPROF", "false", "Serve Go's profiler to admins at /api/v1/system/pprof/ (for finding what is slow)."},
	{"MANGARR_TRUSTED_PROXIES", "private", "Reverse proxies whose X-Forwarded-For/-Proto/-Host headers are believed: addresses or CIDR ranges, comma-separated; private = loopback and private networks; none = no proxy."},
	{"MANGARR_FRAME_ANCESTORS", "'self'", "Sites that may show the UI in a frame (e.g. a dashboard): origins separated by spaces, or * for any (Content-Security-Policy frame-ancestors)."},
	{"MANGARR_KOMGA_LISTEN", ":25600", "Listen address of the Komga-compatible API for reading apps (when enabled in Settings → Reading apps)."},
	{"MANGARR_STORAGE", "local", "Where the library (root folders and recycle bin) is kept: local (the disk) or s3 (an S3-compatible bucket, set with the MANGARR_S3_* variables). The database, caches and download staging stay in the data dir either way. Copy an existing library over with `mangarr storage migrate`."},
	{"MANGARR_S3_ENDPOINT", "", "S3 service URL, e.g. https://s3.example.com (empty: AWS in MANGARR_S3_REGION)."},
	{"MANGARR_S3_BUCKET", "", "Bucket that holds the library."},
	{"MANGARR_S3_REGION", "us-east-1", "Region the requests are signed for (some services want auto)."},
	{"MANGARR_S3_PREFIX", "", "Key prefix, to share a bucket. A file at /data/manga/Title/x.cbz is kept as <prefix>/data/manga/Title/x.cbz."},
	{"MANGARR_S3_ACCESS_KEY_ID", "", "Access key (AWS_ACCESS_KEY_ID is used when unset)."},
	{"MANGARR_S3_SECRET_ACCESS_KEY", "", "Secret key (AWS_SECRET_ACCESS_KEY is used when unset)."},
	{"MANGARR_S3_PUBLIC_ENDPOINT", "", "The S3 service URL as reading devices reach it, for the signed links whole-chapter downloads are sent to (MANGARR_S3_ENDPOINT when empty)."},
	{"MANGARR_S3_REDIRECT_DOWNLOADS", "true", "Send whole-chapter downloads (reading apps' offline downloads) straight to the bucket with a short-lived signed link instead of through the server; false keeps them going through the server."},
	{"MANGARR_S3_PATH_STYLE", "true", "Address the bucket as <endpoint>/<bucket> (most self-hosted services) instead of <bucket>.<endpoint>."},
}

// Modes.
const (
	ModeIntegrated = "integrated"
	ModeServer     = "server"
	ModeUpscaler   = "upscaler"
	// ModeWorker pulls tasks from a server: it listens on nothing.
	ModeWorker = "worker"
)

// Where pages are processed.
const (
	ProcessingLocal   = "local"
	ProcessingWorkers = "workers"
)

// ModeFromEnv returns the process mode; a binary named mangarr-upscaler is a node.
func ModeFromEnv() (string, error) {
	if strings.HasPrefix(filepath.Base(os.Args[0]), "mangarr-upscaler") && os.Getenv("MANGARR_MODE") == "" {
		return ModeUpscaler, nil
	}
	switch m := strings.ToLower(env("MANGARR_MODE", ModeIntegrated)); m {
	case ModeIntegrated, ModeServer, ModeUpscaler, ModeWorker:
		return m, nil
	default:
		return "", fmt.Errorf("MANGARR_MODE must be integrated, server, worker or upscaler (got %q)", m)
	}
}

func Load() (*Config, error) {
	mode, err := ModeFromEnv()
	if err != nil {
		return nil, err
	}
	c := &Config{
		Mode:           mode,
		Listen:         env("MANGARR_LISTEN", ":8787"),
		DataDir:        env("MANGARR_DATA_DIR", "./config"),
		LogLevel:       env("MANGARR_LOG_LEVEL", "info"),
		URLBase:        strings.TrimRight(env("MANGARR_URL_BASE", ""), "/"),
		WebDir:         env("MANGARR_WEB_DIR", ""),
		KomgaListen:    env("MANGARR_KOMGA_LISTEN", ":25600"),
		FrameAncestors: env("MANGARR_FRAME_ANCESTORS", "'self'"),
		Env:            Environ(),
	}
	switch c.Processing = strings.ToLower(env("MANGARR_PROCESSING", ProcessingLocal)); c.Processing {
	case ProcessingLocal, ProcessingWorkers:
	default:
		return nil, fmt.Errorf("MANGARR_PROCESSING must be local or workers (got %q)", c.Processing)
	}
	if c.AuthDisabled, err = envBool("MANGARR_AUTH_DISABLED", false); err != nil {
		return nil, err
	}
	if c.Pprof, err = envBool("MANGARR_PPROF", false); err != nil {
		return nil, err
	}
	if c.TrustedProxies, err = clientip.Parse(env("MANGARR_TRUSTED_PROXIES", "private")); err != nil {
		return nil, fmt.Errorf("MANGARR_TRUSTED_PROXIES: %w", err)
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	c.DataDir = abs
	c.DB, c.DBSource = DefaultDB(c.DataDir), "default"
	if v := env("MANGARR_DB", ""); v != "" {
		c.DB, c.DBSource = v, "env"
	} else if b, err := os.ReadFile(filepath.Join(c.DataDir, DBFileName)); err == nil && strings.TrimSpace(string(b)) != "" {
		c.DB, c.DBSource = strings.TrimSpace(string(b)), "file"
	}
	switch d := env("MANGARR_LOG_DIR", filepath.Join(c.DataDir, "logs")); strings.ToLower(d) {
	case "off", "none", "false", "0":
	default:
		c.LogDir = d
	}
	if c.LogFiles, err = strconv.Atoi(env("MANGARR_LOG_FILES", "5")); err != nil || c.LogFiles < 1 {
		return nil, fmt.Errorf("MANGARR_LOG_FILES must be a positive number")
	}
	if c.URLBase != "" && !strings.HasPrefix(c.URLBase, "/") {
		c.URLBase = "/" + c.URLBase
	}
	if c.Storage, err = StorageFromEnv(c.DataDir); err != nil {
		return nil, err
	}
	return c, nil
}

// StorageFromEnv reads MANGARR_STORAGE and the MANGARR_S3_* variables; nil
// means the library stays on the local disk.
func StorageFromEnv(dataDir string) (*libfs.S3Config, error) {
	switch m := strings.ToLower(env("MANGARR_STORAGE", "local")); m {
	case "local":
		return nil, nil
	case "s3":
	default:
		return nil, fmt.Errorf("MANGARR_STORAGE must be local or s3 (got %q)", m)
	}
	pathStyle, err := envBool("MANGARR_S3_PATH_STYLE", true)
	if err != nil {
		return nil, err
	}
	redirect, err := envBool("MANGARR_S3_REDIRECT_DOWNLOADS", true)
	if err != nil {
		return nil, err
	}
	cfg := &libfs.S3Config{
		Endpoint:  env("MANGARR_S3_ENDPOINT", ""),
		Bucket:    env("MANGARR_S3_BUCKET", ""),
		Region:    env("MANGARR_S3_REGION", "us-east-1"),
		Prefix:    env("MANGARR_S3_PREFIX", ""),
		AccessKey: env("MANGARR_S3_ACCESS_KEY_ID", os.Getenv("AWS_ACCESS_KEY_ID")),
		SecretKey: env("MANGARR_S3_SECRET_ACCESS_KEY", os.Getenv("AWS_SECRET_ACCESS_KEY")),
		PathStyle: pathStyle,
		TempDir:   filepath.Join(dataDir, "tmp", "uploads"),

		PublicEndpoint: env("MANGARR_S3_PUBLIC_ENDPOINT", ""),
		NoRedirect:     !redirect,
	}
	switch {
	case cfg.Bucket == "":
		return nil, errors.New("MANGARR_STORAGE=s3 needs MANGARR_S3_BUCKET")
	case cfg.AccessKey == "" || cfg.SecretKey == "":
		return nil, errors.New("MANGARR_STORAGE=s3 needs MANGARR_S3_ACCESS_KEY_ID and MANGARR_S3_SECRET_ACCESS_KEY")
	}
	return cfg, nil
}

// Environ returns the MANGARR_* variables of the process.
func Environ() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(k, "MANGARR_") {
			out[k] = v
		}
	}
	return out
}

// Dir returns a subdirectory of DataDir, creating it if needed.
func (c *Config) Dir(name string) (string, error) {
	p := filepath.Join(c.DataDir, name)
	if err := os.MkdirAll(p, 0o775); err != nil {
		return "", err
	}
	return p, nil
}

// DBFileName is the file in the data dir that holds the database DSN after
// the database was moved from the UI (used when MANGARR_DB isn't set).
const DBFileName = "database.dsn"

// DefaultDB is the SQLite database in the data dir.
func DefaultDB(dataDir string) string { return "sqlite://" + filepath.Join(dataDir, "mangarr.db") }

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}
