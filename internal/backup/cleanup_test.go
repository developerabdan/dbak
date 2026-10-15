package backup

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRemoveStaleCredentials(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	files := map[string]time.Duration{
		"pgpass-old":    2 * time.Hour,
		"my-old.cnf":    90 * time.Minute,
		"pgpass-new":    10 * time.Minute,
		"my-new.cnf":    59 * time.Minute,
		"state.json":    48 * time.Hour,
		"notes-old.cnf": 48 * time.Hour,
	}
	for name, age := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := RemoveStaleCredentials(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range removed {
		names = append(names, filepath.Base(f))
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"my-old.cnf", "pgpass-old"}) {
		t.Fatalf("got removed %v", names)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 4 {
		t.Fatalf("got %d files, want 4", len(left))
	}
}
