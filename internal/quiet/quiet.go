// Package quiet evaluates schedule windows ("quiet hours") that pause
// downloads or processing, or tighten throttling, at certain times.
package quiet

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/settings"
)

// Effects are the combined effects of the windows active at a moment.
type Effects struct {
	PauseDownloads  bool     `json:"pauseDownloads"`
	PauseProcessing bool     `json:"pauseProcessing"`
	Throttle        string   `json:"throttle,omitempty"`
	Windows         []string `json:"windows,omitempty"`
}

var days = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Validate checks a schedule.
func Validate(s settings.Schedule) error { return s.Validate() }

// strength orders throttle presets; the gentlest active one wins.
var strength = map[string]int{"fast": 1, "normal": 2, "gentle": 3}

// Evaluate returns the effects of the windows active at now.
func Evaluate(s settings.Schedule, now time.Time) Effects {
	loc := Location(s.Timezone)
	t := now.In(loc)
	minute := t.Hour()*60 + t.Minute()
	today := days[t.Weekday()]
	yesterday := days[(int(t.Weekday())+6)%7]
	var e Effects
	for _, w := range s.Windows {
		start, err1 := settings.ParseClock(w.Start)
		end, err2 := settings.ParseClock(w.End)
		if err1 != nil || err2 != nil || start == end {
			continue
		}
		on := func(day string) bool {
			return len(w.Days) == 0 || slices.ContainsFunc(w.Days, func(d string) bool { return strings.EqualFold(d, day) })
		}
		active := false
		if start < end {
			active = on(today) && minute >= start && minute < end
		} else { // crosses midnight: the part after midnight belongs to the previous day
			active = (on(today) && minute >= start) || (on(yesterday) && minute < end)
		}
		if !active {
			continue
		}
		e.PauseDownloads = e.PauseDownloads || w.PauseDownloads
		e.PauseProcessing = e.PauseProcessing || w.PauseProcessing
		if strength[w.Throttle] > strength[e.Throttle] {
			e.Throttle = w.Throttle
		}
		name := w.Name
		if name == "" {
			name = w.Start + "–" + w.End
		}
		e.Windows = append(e.Windows, name)
	}
	return e
}

// Location resolves the schedule timezone, falling back to the server timezone.
func Location(zone string) *time.Location {
	loc := time.Local
	if zone != "" {
		if l, err := time.LoadLocation(zone); err == nil {
			loc = l
		}
	}
	return loc
}

// ZoneName names a location the way browsers understand it (an IANA name like
// "Europe/Kyiv"). The server's own zone is looked up from TZ or /etc/localtime;
// it is "" when that cannot be told, which callers read as the server's zone.
func ZoneName(loc *time.Location) string {
	if loc != time.Local {
		return loc.String()
	}
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok {
			if _, err := time.LoadLocation(name); err == nil {
				return name
			}
		}
	}
	return ""
}
