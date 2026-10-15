package backup

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListBackups(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "vm-app-01", "main_pg", "app_prod")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, dir,
		"app_prod_20261013T190000Z.dump",
		"app_prod_20261015T190000Z.dump",
		"app_prod_20261014T190000Z.sql.zst",
		"app_prod_20261016T020000Z.dump.age",
		"app_prod_20261016T190000Z.dump.partial",
		"notes.txt",
	)
	entries, err := ListBackups(localStore{}, root, "vm-app-01", "main_pg", "app_prod")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	want := []string{"app_prod_20261016T020000Z.dump.age", "app_prod_20261015T190000Z.dump", "app_prod_20261014T190000Z.sql.zst", "app_prod_20261013T190000Z.dump"}
	if !slices.Equal(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	if entries[0].SizeBytes != 1 || entries[0].Source != "main_pg" || entries[0].Path != filepath.Join(dir, want[0]) {
		t.Fatalf("got %+v", entries[0])
	}
	missing, err := ListBackups(localStore{}, root, "vm-app-01", "main_pg", "app_audit")
	if err != nil || len(missing) != 0 {
		t.Fatalf("got %v %v for a missing directory", missing, err)
	}
}
