package schedule

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"dbak/internal/config"
)

var weekdayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

type Plan struct {
	Frequency  string
	Weekday    time.Weekday
	Hour       int
	Minute     int
	EveryHours int
	Zone       string
}

func NewPlan(sc config.Schedule) (Plan, error) {
	if _, err := time.LoadLocation(sc.Timezone); err != nil || sc.Timezone == "" {
		return Plan{}, fmt.Errorf("the timezone %q is not known", sc.Timezone)
	}
	p := Plan{Frequency: sc.Frequency, Zone: sc.Timezone}
	switch sc.Frequency {
	case config.FrequencyEveryNHours:
		if sc.EveryHours < 1 || 24%sc.EveryHours != 0 {
			return Plan{}, fmt.Errorf("the interval of %d hours is not supported", sc.EveryHours)
		}
		p.EveryHours = sc.EveryHours
		return p, nil
	case config.FrequencyWeekly:
		day := slices.Index(weekdayNames, sc.Weekday)
		if day < 0 {
			return Plan{}, fmt.Errorf("the day %q is not known", sc.Weekday)
		}
		p.Weekday = time.Weekday(day)
	case config.FrequencyDaily:
	default:
		return Plan{}, fmt.Errorf("the frequency %q is not known", sc.Frequency)
	}
	hour, minute, err := config.ParseClock(sc.Time)
	if err != nil {
		return Plan{}, err
	}
	p.Hour, p.Minute = hour, minute
	return p, nil
}

func (p Plan) location() *time.Location {
	loc, err := time.LoadLocation(p.Zone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (p Plan) In(zone string, now time.Time) (Plan, error) {
	to, err := time.LoadLocation(zone)
	if err != nil || zone == "" {
		return Plan{}, fmt.Errorf("the timezone %q is not known", zone)
	}
	next := p.Next(now).In(to)
	out := p
	out.Zone = zone
	out.Hour, out.Minute, out.Weekday = next.Hour(), next.Minute(), next.Weekday()
	if p.Frequency == config.FrequencyEveryNHours {
		out.Hour = next.Hour() % p.EveryHours
	}
	return out, nil
}

func (p Plan) hours() []int {
	if p.Frequency != config.FrequencyEveryNHours {
		return []int{p.Hour}
	}
	var hours []int
	for h := p.Hour; h < 24; h += p.EveryHours {
		hours = append(hours, h)
	}
	return hours
}

func (p Plan) Next(after time.Time) time.Time {
	loc := p.location()
	local := after.In(loc)
	for offset := 0; offset <= 8; offset++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+offset, 0, 0, 0, 0, loc)
		if p.Frequency == config.FrequencyWeekly && day.Weekday() != p.Weekday {
			continue
		}
		for _, h := range p.hours() {
			candidate := time.Date(day.Year(), day.Month(), day.Day(), h, p.Minute, 0, 0, loc)
			if candidate.After(after) {
				return candidate
			}
		}
	}
	return time.Time{}
}

func (p Plan) OnCalendar(withZone bool) string {
	var expr string
	switch p.Frequency {
	case config.FrequencyEveryNHours:
		expr = fmt.Sprintf("*-*-* %02d/%d:%02d:00", p.Hour, p.EveryHours, p.Minute)
	case config.FrequencyWeekly:
		expr = fmt.Sprintf("%s *-*-* %02d:%02d:00", weekdayNames[p.Weekday], p.Hour, p.Minute)
	default:
		expr = fmt.Sprintf("*-*-* %02d:%02d:00", p.Hour, p.Minute)
	}
	if withZone {
		expr += " " + p.Zone
	}
	return expr
}

func (p Plan) CronFields() string {
	switch p.Frequency {
	case config.FrequencyEveryNHours:
		hours := fmt.Sprintf("*/%d", p.EveryHours)
		if p.Hour != 0 {
			hours = fmt.Sprintf("%d-23/%d", p.Hour, p.EveryHours)
		}
		return fmt.Sprintf("%d %s * * *", p.Minute, hours)
	case config.FrequencyWeekly:
		return fmt.Sprintf("%d %d * * %d", p.Minute, p.Hour, int(p.Weekday))
	}
	return fmt.Sprintf("%d %d * * *", p.Minute, p.Hour)
}

func (p Plan) Describe() string {
	clock := fmt.Sprintf("%02d:%02d", p.Hour, p.Minute)
	switch p.Frequency {
	case config.FrequencyEveryNHours:
		hours := make([]string, 0, 24/p.EveryHours)
		for _, h := range p.hours() {
			hours = append(hours, fmt.Sprintf("%02d:%02d", h, p.Minute))
		}
		return fmt.Sprintf("every %d hours (%s) %s", p.EveryHours, strings.Join(hours, ", "), p.Zone)
	case config.FrequencyWeekly:
		return fmt.Sprintf("weekly on %s at %s %s", weekdayNames[p.Weekday], clock, p.Zone)
	}
	return fmt.Sprintf("daily at %s %s", clock, p.Zone)
}

func UsesDaylightSaving(zone string, year int) bool {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return false
	}
	_, jan := time.Date(year, time.January, 1, 12, 0, 0, 0, loc).Zone()
	_, jul := time.Date(year, time.July, 1, 12, 0, 0, 0, loc).Zone()
	return jan != jul
}
