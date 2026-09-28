package jobs

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

type Schedule = TaskSchedule

type TaskSchedule struct {
	Kind            string   `json:"kind"`
	IntervalMinutes int      `json:"intervalMinutes,omitempty"`
	TimesOfDay      []string `json:"timesOfDay,omitempty"`
	Weekdays        []string `json:"weekdays,omitempty"`
}

var weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

func (v Schedule) Validate(minMinutes int) error {
	switch v.Kind {
	case "interval":
		if v.IntervalMinutes < minMinutes || v.IntervalMinutes > 525600 {
			return fmt.Errorf("interval must be between %d and 525600 minutes", minMinutes)
		}
		if len(v.TimesOfDay) != 0 || len(v.Weekdays) != 0 {
			return fmt.Errorf("interval schedules cannot contain times or weekdays")
		}
	case "daily":
		if v.IntervalMinutes != 0 {
			return fmt.Errorf("daily schedules cannot contain an interval")
		}
		if len(v.TimesOfDay) == 0 || len(v.TimesOfDay) > 24 {
			return fmt.Errorf("provide between 1 and 24 times of day")
		}
		seen := map[string]bool{}
		for _, clock := range v.TimesOfDay {
			if parsed, err := time.Parse("15:04", clock); err != nil || parsed.Format("15:04") != clock {
				return fmt.Errorf("invalid time %q; use HH:MM", clock)
			}
			if seen[clock] {
				return fmt.Errorf("duplicate time %q", clock)
			}
			seen[clock] = true
		}
		seen = map[string]bool{}
		for _, day := range v.Weekdays {
			if !slices.Contains(weekdays, day) || seen[day] {
				return fmt.Errorf("invalid or duplicate weekday %q", day)
			}
			seen[day] = true
		}
	default:
		return fmt.Errorf("schedule kind must be interval or daily")
	}
	return nil
}

func DefaultSchedule(row model.ScheduledTask) Schedule {
	return Schedule{Kind: "interval", IntervalMinutes: row.IntervalMinutes}
}

func EffectiveSchedule(row model.ScheduledTask) Schedule {
	if row.ScheduleKind == nil {
		return DefaultSchedule(row)
	}
	v := Schedule{Kind: *row.ScheduleKind, TimesOfDay: row.TimesOfDay, Weekdays: row.Weekdays}
	if row.CustomIntervalMinutes != nil {
		v.IntervalMinutes = *row.CustomIntervalMinutes
	}
	return v
}

func CustomSchedule(row model.ScheduledTask) bool {
	v := EffectiveSchedule(row)
	return v.Kind != "interval" || v.IntervalMinutes != row.IntervalMinutes
}

// slots returns instants strictly after the anchor. time.Date gives each local
// wall-clock slot one instant, including when DST skips or repeats an hour.
func slots(v Schedule, after time.Time, n int, loc *time.Location) []time.Time {
	out := make([]time.Time, 0, n)
	if v.Kind == "interval" {
		for i := 1; i <= n; i++ {
			out = append(out, after.Add(time.Duration(i)*time.Duration(v.IntervalMinutes)*time.Minute).In(loc))
		}
		return out
	}
	local := after.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc)
	for d := 0; d < 8*(n+1) && len(out) < n; d++ {
		date := day.AddDate(0, 0, d)
		if len(v.Weekdays) > 0 && !slices.Contains(v.Weekdays, weekdays[date.Weekday()]) {
			continue
		}
		candidates := []time.Time{}
		for _, clock := range v.TimesOfDay {
			minutes, err := settings.ParseClock(clock)
			if err != nil {
				continue
			}
			slot := time.Date(date.Year(), date.Month(), date.Day(), minutes/60, minutes%60, 0, 0, loc)
			if slot.After(after) {
				candidates = append(candidates, slot)
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].Before(candidates[j]) })
		for _, slot := range candidates {
			if len(out) == 0 || !slot.Equal(out[len(out)-1]) {
				out = append(out, slot)
			}
			if len(out) == n {
				break
			}
		}
	}
	return out
}
