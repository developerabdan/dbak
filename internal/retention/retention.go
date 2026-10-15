package retention

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	timestampLayout = "20060102T150405Z"
	partialSuffix   = ".partial"
	PartialMaxAge   = 24 * time.Hour
)

var extensions = []string{"dump", "sql.zst"}

var namePattern = regexp.MustCompile(`^([A-Za-z0-9_]+)_(\d{8}T\d{6}Z)\.(dump|sql\.zst)$`)

type Rules struct {
	Daily     int
	Weekly    int
	Monthly   int
	KeepToday bool
}

type File struct {
	Name    string
	Time    time.Time
	Partial bool
}

func Parse(database, name string) (File, bool) {
	partial := strings.HasSuffix(name, partialSuffix)
	m := namePattern.FindStringSubmatch(strings.TrimSuffix(name, partialSuffix))
	if m == nil || m[1] != database || !slices.Contains(extensions, m[3]) {
		return File{}, false
	}
	at, err := time.Parse(timestampLayout, m[2])
	if err != nil {
		return File{}, false
	}
	return File{Name: name, Time: at, Partial: partial}, true
}

func Remove(files []File, rules Rules, loc *time.Location, now time.Time) []File {
	var complete, remove []File
	for _, f := range files {
		switch {
		case !f.Partial:
			complete = append(complete, f)
		case now.Sub(f.Time) > PartialMaxAge:
			remove = append(remove, f)
		}
	}
	slices.SortStableFunc(complete, func(a, b File) int { return b.Time.Compare(a.Time) })
	keep := keepSet(complete, rules, loc, now)
	for i, f := range complete {
		if !keep[i] {
			remove = append(remove, f)
		}
	}
	return remove
}

func keepSet(sorted []File, rules Rules, loc *time.Location, now time.Time) []bool {
	keep := make([]bool, len(sorted))
	if len(sorted) == 0 {
		return keep
	}
	keep[0] = true
	today := dayKey(now.In(loc))
	daily := func(t time.Time) string { return dayKey(t) }
	if rules.KeepToday {
		for i, f := range sorted {
			if dayKey(f.Time.In(loc)) == today {
				keep[i] = true
			}
		}
		daily = func(t time.Time) string {
			if dayKey(t) == today {
				return ""
			}
			return dayKey(t)
		}
	}
	mark(sorted, keep, rules.Daily, loc, daily)
	mark(sorted, keep, rules.Weekly, loc, weekKey)
	mark(sorted, keep, rules.Monthly, loc, monthKey)
	return keep
}

func mark(sorted []File, keep []bool, count int, loc *time.Location, key func(time.Time) string) {
	seen := map[string]bool{}
	for i, f := range sorted {
		if len(seen) >= count {
			return
		}
		k := key(f.Time.In(loc))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		keep[i] = true
	}
}

func dayKey(t time.Time) string {
	return t.Format("2006-01-02")
}

func weekKey(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}

func monthKey(t time.Time) string {
	return t.Format("2006-01")
}
