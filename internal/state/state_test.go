package state

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendKeepsLastRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	start := time.Date(2026, 10, 15, 19, 0, 0, 0, time.UTC)
	for i := range MaxRuns + 5 {
		run := Run{Host: fmt.Sprintf("run%d", i), Status: StatusSuccess, StartedAt: start.Add(time.Duration(i) * time.Hour)}
		if err := Append(path, run); err != nil {
			t.Fatal(err)
		}
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Runs) != MaxRuns {
		t.Fatalf("got %d runs, want %d", len(f.Runs), MaxRuns)
	}
	if f.Runs[0].Host != "run5" || f.Runs[MaxRuns-1].Host != fmt.Sprintf("run%d", MaxRuns+4) {
		t.Fatalf("wrong runs kept: first %s, last %s", f.Runs[0].Host, f.Runs[MaxRuns-1].Host)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("got mode %v, want 0600", info.Mode().Perm())
	}
}

func TestLoadMissingFileGivesEmptyHistory(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil || len(f.Runs) != 0 {
		t.Fatalf("got %v %v, want an empty history", f, err)
	}
}

func TestOverallStatus(t *testing.T) {
	ok := Database{Status: StatusSuccess}
	bad := Database{Status: StatusFailure}
	cases := []struct {
		name string
		dbs  []Database
		want string
	}{
		{"all databases succeed", []Database{ok, ok}, StatusSuccess},
		{"some databases fail", []Database{ok, bad}, StatusPartial},
		{"all databases fail", []Database{bad, bad}, StatusFailure},
		{"no databases", nil, StatusFailure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OverallStatus(c.dbs); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}
