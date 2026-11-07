package komgaapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errorDTO is Spring's error body; Paperback shows violations[].message.
type errorDTO struct {
	Timestamp  string         `json:"timestamp"`
	Status     int            `json:"status"`
	Error      string         `json:"error"`
	Message    string         `json:"message"`
	Path       string         `json:"path"`
	Violations []violationDTO `json:"violations"`
}

type violationDTO struct {
	FieldName string `json:"fieldName"`
	Message   string `json:"message"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, status, errorDTO{Timestamp: komgaTime(time.Now()), Status: status, Error: http.StatusText(status), Message: msg,
		Path: r.URL.Path, Violations: []violationDTO{{Message: msg}}})
}

// komgaTime formats timestamps like Komga (UTC, second precision, 'Z').
func komgaTime(t time.Time) string {
	if t.IsZero() {
		t = time.Unix(0, 0)
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func queryInt(r *http.Request, name string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return v
	}
	return def
}

func queryBool(r *http.Request, name string) bool {
	v, _ := strconv.ParseBool(r.URL.Query().Get(name))
	return v
}
