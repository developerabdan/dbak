package retention

import (
	"slices"
	"testing"
	"time"
)

func at(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func file(value string) File {
	t := at(value)
	return File{Name: "app_" + t.UTC().Format(timestampLayout) + ".dump", Time: t}
}

func days(first string, count int, clock string) []File {
	start := at(first + "T" + clock + ":00Z")
	files := make([]File, 0, count)
	for i := range count {
		t := start.AddDate(0, 0, i)
		files = append(files, File{Name: "app_" + t.Format(timestampLayout) + ".dump", Time: t})
	}
	return files
}

func names(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Time.UTC().Format("2006-01-02T15:04"))
	}
	slices.Sort(out)
	return out
}

func kept(files, removed []File) []string {
	var out []File
	for _, f := range files {
		if !slices.ContainsFunc(removed, func(r File) bool { return r.Name == f.Name }) {
			out = append(out, f)
		}
	}
	return names(out)
}

func TestRemove(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	cases := []struct {
		name  string
		files []File
		rules Rules
		loc   *time.Location
		now   time.Time
		want  []string
	}{
		{
			name:  "daily keeps the last N days",
			files: days("2026-10-01", 15, "19:00"),
			rules: Rules{Daily: 7},
			loc:   time.UTC,
			now:   at("2026-10-15T20:00:00Z"),
			want:  []string{"2026-10-09T19:00", "2026-10-10T19:00", "2026-10-11T19:00", "2026-10-12T19:00", "2026-10-13T19:00", "2026-10-14T19:00", "2026-10-15T19:00"},
		},
		{
			name:  "daily counts only days that have a backup",
			files: []File{file("2026-10-01T19:00:00Z"), file("2026-10-05T19:00:00Z"), file("2026-10-10T19:00:00Z"), file("2026-10-15T19:00:00Z")},
			rules: Rules{Daily: 3},
			loc:   time.UTC,
			now:   at("2026-10-15T20:00:00Z"),
			want:  []string{"2026-10-05T19:00", "2026-10-10T19:00", "2026-10-15T19:00"},
		},
		{
			name:  "daily keeps the newest backup of a day",
			files: []File{file("2026-10-14T01:00:00Z"), file("2026-10-14T13:00:00Z"), file("2026-10-15T01:00:00Z"), file("2026-10-15T13:00:00Z")},
			rules: Rules{Daily: 2},
			loc:   time.UTC,
			now:   at("2026-10-15T20:00:00Z"),
			want:  []string{"2026-10-14T13:00", "2026-10-15T13:00"},
		},
		{
			name:  "days use the timezone of the config",
			files: []File{file("2026-10-14T16:00:00Z"), file("2026-10-14T18:00:00Z"), file("2026-10-15T16:00:00Z")},
			rules: Rules{Daily: 2},
			loc:   jakarta,
			now:   at("2026-10-15T20:00:00Z"),
			want:  []string{"2026-10-14T16:00", "2026-10-15T16:00"},
		},
		{
			name:  "weekly uses ISO weeks",
			files: days("2026-09-21", 25, "19:00"),
			rules: Rules{Weekly: 3},
			loc:   time.UTC,
			now:   at("2026-10-15T20:00:00Z"),
			want:  []string{"2026-10-04T19:00", "2026-10-11T19:00", "2026-10-15T19:00"},
		},
		{
			name:  "weekly over the end of the year",
			files: days("2026-12-20", 20, "19:00"),
			rules: Rules{Weekly: 3},
			loc:   time.UTC,
			now:   at("2027-01-08T20:00:00Z"),
			want:  []string{"2026-12-27T19:00", "2027-01-03T19:00", "2027-01-08T19:00"},
		},
		{
			name:  "monthly over the end of the year",
			files: days("2026-10-01", 120, "19:00"),
			rules: Rules{Monthly: 3},
			loc:   time.UTC,
			now:   at("2027-01-28T20:00:00Z"),
			want:  []string{"2026-11-30T19:00", "2026-12-31T19:00", "2027-01-28T19:00"},
		},
		{
			name:  "months use the timezone of the config",
			files: []File{file("2026-09-30T16:00:00Z"), file("2026-09-30T18:00:00Z"), file("2026-10-15T19:00:00Z")},
			rules: Rules{Monthly: 2},
			loc:   jakarta,
			now:   at("2026-10-15T20:00:00Z"),
			want:  []string{"2026-09-30T16:00", "2026-10-15T19:00"},
		},
		{
			name:  "one file can satisfy more than one rule",
			files: days("2026-09-01", 45, "19:00"),
			rules: Rules{Daily: 2, Weekly: 2, Monthly: 2},
			loc:   time.UTC,
			now:   at("2026-10-15T20:00:00Z"),
			want:  []string{"2026-09-30T19:00", "2026-10-11T19:00", "2026-10-14T19:00", "2026-10-15T19:00"},
		},
		{
			name:  "the newest backup is never removed",
			files: days("2026-10-01", 5, "19:00"),
			rules: Rules{Monthly: 0},
			loc:   time.UTC,
			now:   at("2026-10-15T20:00:00Z"),
			want:  []string{"2026-10-05T19:00"},
		},
		{
			name: "every N hours keeps all backups of today",
			files: []File{
				file("2026-10-13T00:00:00Z"), file("2026-10-13T06:00:00Z"),
				file("2026-10-14T00:00:00Z"), file("2026-10-14T06:00:00Z"),
				file("2026-10-15T00:00:00Z"), file("2026-10-15T06:00:00Z"), file("2026-10-15T12:00:00Z"),
			},
			rules: Rules{Daily: 1, KeepToday: true},
			loc:   time.UTC,
			now:   at("2026-10-15T13:00:00Z"),
			want:  []string{"2026-10-14T06:00", "2026-10-15T00:00", "2026-10-15T06:00", "2026-10-15T12:00"},
		},
		{
			name:  "no files",
			rules: Rules{Daily: 7},
			loc:   time.UTC,
			now:   at("2026-10-15T20:00:00Z"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			removed := Remove(c.files, c.rules, c.loc, c.now)
			if got := kept(c.files, removed); !slices.Equal(got, c.want) {
				t.Fatalf("kept %v, want %v", got, c.want)
			}
		})
	}
}

func TestRemovePartialFiles(t *testing.T) {
	old := File{Name: "app_20261014T100000Z.dump.partial", Time: at("2026-10-14T10:00:00Z"), Partial: true}
	recent := File{Name: "app_20261015T100000Z.dump.partial", Time: at("2026-10-15T10:00:00Z"), Partial: true}
	complete := file("2026-10-15T09:00:00Z")
	removed := Remove([]File{old, recent, complete}, Rules{Daily: 7}, time.UTC, at("2026-10-15T12:00:00Z"))
	if len(removed) != 1 || removed[0].Name != old.Name {
		t.Fatalf("got %v, want only the old partial file", removed)
	}
}

func TestRemoveKeepsAllNewestFiles(t *testing.T) {
	newest := at("2026-10-15T19:00:00Z")
	plain := File{Name: "app_20261015T190000Z.dump", Time: newest}
	sealed := File{Name: "app_20261015T190000Z.dump.age", Time: newest}
	older := file("2026-10-15T07:00:00Z")
	removed := Remove([]File{older, plain, sealed}, Rules{Daily: 1}, time.UTC, at("2026-10-15T20:00:00Z"))
	if len(removed) != 1 || removed[0].Name != older.Name {
		t.Fatalf("got %v, want only the older file", removed)
	}
}

func TestParse(t *testing.T) {
	good := map[string]File{
		"app_prod_20261015T190000Z.dump":             {Name: "app_prod_20261015T190000Z.dump", Time: at("2026-10-15T19:00:00Z")},
		"app_prod_20261015T190000Z.sql.zst":          {Name: "app_prod_20261015T190000Z.sql.zst", Time: at("2026-10-15T19:00:00Z")},
		"app_prod_20261015T190000Z.dump.partial":     {Name: "app_prod_20261015T190000Z.dump.partial", Time: at("2026-10-15T19:00:00Z"), Partial: true},
		"app_prod_20261015T190000Z.sql.zst.partial":  {Name: "app_prod_20261015T190000Z.sql.zst.partial", Time: at("2026-10-15T19:00:00Z"), Partial: true},
		"app_prod_20261015T190000Z.dump.age":         {Name: "app_prod_20261015T190000Z.dump.age", Time: at("2026-10-15T19:00:00Z")},
		"app_prod_20261015T190000Z.sql.zst.age":      {Name: "app_prod_20261015T190000Z.sql.zst.age", Time: at("2026-10-15T19:00:00Z")},
		"app_prod_20261015T190000Z.dump.age.partial": {Name: "app_prod_20261015T190000Z.dump.age.partial", Time: at("2026-10-15T19:00:00Z"), Partial: true},
	}
	for name, want := range good {
		got, ok := Parse("app_prod", name)
		if !ok || got.Name != want.Name || !got.Time.Equal(want.Time) || got.Partial != want.Partial {
			t.Fatalf("got %+v %v for %q", got, ok, name)
		}
	}
	bad := []string{
		"app_prod_20261015T190000Z.dump.bak",
		"app_prod_20261015T190000Z.age",
		"app_prod_20261015T190000Z.dump.age.age",
		"app_prod_20261015T190000Z.sql.age",
		"app_prod_20261015T190000Z.sql",
		"app_prod_20261015T190000.dump",
		"app_prod_20261315T190000Z.dump",
		"other_20261015T190000Z.dump",
		"app_prod_extra_20261015T190000Z.dump",
		"notes.txt",
		".dbak-write-check",
	}
	for _, name := range bad {
		if _, ok := Parse("app_prod", name); ok {
			t.Fatalf("got a match for %q", name)
		}
	}
}
