package worker

import (
	"fmt"
	"strconv"
	"strings"
)

// Vars are a worker's environment variables: name, old name, default.
// They are documented from here (docs/configuration.md).
var Vars = [][3]string{
	{"MANGARR_SERVER_URL", "", ""},
	{"MANGARR_WORKER_KEY", "", ""},
	{"MANGARR_WORKER_ROLES", "", "download,upscale,encode"},
	{"MANGARR_WORKER_CONCURRENT", "", "0"},
	{"MANGARR_WORKER_PREFETCH", "", "0"},
	{"MANGARR_WORKER_PAGE_CONCURRENCY", "", "0"},
	{"MANGARR_WORKER_SHARED_STORAGE", "", "false"},
	{"MANGARR_WORKER_AUTO_UPDATE", "", "true"},
}

// Help describes each variable for the configuration reference.
var Help = map[string]string{
	"MANGARR_SERVER_URL":              "The mangarr server this worker asks for work.",
	"MANGARR_WORKER_KEY":              "This worker's own key, from System → Workers.",
	"MANGARR_WORKER_ROLES":            "What it offers to do (the server narrows this to what the worker may do).",
	"MANGARR_WORKER_CONCURRENT":       "Tasks it takes at once (0 = what System → Workers says, applied without a restart).",
	"MANGARR_WORKER_PREFETCH":         "Pages it fetches ahead of its uploads (0 = what the server says).",
	"MANGARR_WORKER_PAGE_CONCURRENCY": "Pages it fetches at a time when System → Workers leaves it at 0 (0 = 4).",
	"MANGARR_WORKER_SHARED_STORAGE":   "true when this worker sees the server's data folder at the same path (the same volume mounted at /config): it then reads and writes pages there instead of sending them over HTTP. Falls back to HTTP for any task whose files it can't see.",
	"MANGARR_WORKER_AUTO_UPDATE":      "false keeps a desktop worker on its version when the server is newer (it only says an update is waiting). A worker in a container never replaces itself: its image is updated from outside.",
}

// LoadConfig reads a worker's configuration from the environment.
func LoadConfig(getenv func(string) string) (Config, error) {
	get := func(name string) string {
		for _, v := range Vars {
			if v[0] != name {
				continue
			}
			if x := getenv(v[0]); x != "" {
				return x
			}
			if v[1] != "" {
				if x := getenv(v[1]); x != "" {
					return x
				}
			}
			return v[2]
		}
		return ""
	}
	c := Config{ServerURL: strings.TrimRight(get("MANGARR_SERVER_URL"), "/"), Key: get("MANGARR_WORKER_KEY")}
	for _, r := range strings.Split(get("MANGARR_WORKER_ROLES"), ",") {
		if r = strings.TrimSpace(r); r != "" {
			c.Roles = append(c.Roles, r)
		}
	}
	var err error
	if c.Concurrent, err = strconv.Atoi(get("MANGARR_WORKER_CONCURRENT")); err != nil {
		return c, fmt.Errorf("MANGARR_WORKER_CONCURRENT: %w", err)
	}
	if c.Prefetch, err = strconv.Atoi(get("MANGARR_WORKER_PREFETCH")); err != nil {
		return c, fmt.Errorf("MANGARR_WORKER_PREFETCH: %w", err)
	}
	if c.PageConcurrency, err = strconv.Atoi(get("MANGARR_WORKER_PAGE_CONCURRENCY")); err != nil {
		return c, fmt.Errorf("MANGARR_WORKER_PAGE_CONCURRENCY: %w", err)
	}
	if v := get("MANGARR_WORKER_SHARED_STORAGE"); v != "" {
		if c.SharedStorage, err = strconv.ParseBool(v); err != nil {
			return c, fmt.Errorf("MANGARR_WORKER_SHARED_STORAGE: %w", err)
		}
	}
	if c.AutoUpdate, err = strconv.ParseBool(get("MANGARR_WORKER_AUTO_UPDATE")); err != nil {
		return c, fmt.Errorf("MANGARR_WORKER_AUTO_UPDATE: %w", err)
	}
	if c.ServerURL == "" {
		return c, fmt.Errorf("MANGARR_SERVER_URL: a worker needs the address of its server")
	}
	if c.Key == "" {
		return c, fmt.Errorf("MANGARR_WORKER_KEY: a worker needs its own key (make one in System → Workers)")
	}
	return c, nil
}
