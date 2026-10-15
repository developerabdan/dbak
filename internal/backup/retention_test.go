package backup

import (
	"bytes"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/state"
)

type localStore struct{}

func (localStore) ReadDir(dir string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (localStore) Remove(path string) error {
	return os.Remove(path)
}

func touch(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func listNames(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && p != dir {
			names = append(names, d.Name())
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	return names
}

func TestRetentionApply(t *testing.T) {
	root := t.TempDir()
	prodDir := filepath.Join(root, "vm-app-01", "main_pg", "app_prod")
	auditDir := filepath.Join(root, "vm-app-01", "main_pg", "app_audit")
	for _, dir := range []string{prodDir, auditDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch(t, prodDir,
		"app_prod_20261012T190000Z.dump",
		"app_prod_20261013T190000Z.dump",
		"app_prod_20261014T190000Z.dump",
		"app_prod_20261015T190000Z.dump",
		"app_prod_20261013T190000Z.dump.partial",
		"app_prod_20261015T190000Z.dump.partial",
		"app_prod_20261001T190000Z.dump.bak",
		"notes.txt",
	)
	if err := os.Mkdir(filepath.Join(prodDir, "app_prod_20261001T190000Z.dump"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, auditDir, "app_audit_20261012T190000Z.dump", "app_audit_20261015T190000Z.dump")

	cfg := &config.Config{
		Destination: config.Destination{RemotePath: root},
		Schedule:    config.Schedule{Frequency: config.FrequencyDaily, Timezone: "Asia/Jakarta"},
		Retention:   config.Retention{Daily: 2},
	}
	var logs bytes.Buffer
	r, err := NewRetention(cfg, config.Source{Name: "main_pg"}, localStore{}, "vm-app-01", slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r.Now = func() time.Time { return time.Date(2026, 10, 15, 20, 0, 0, 0, time.UTC) }
	r.Apply([]Result{
		{Database: state.Database{Name: "app_prod", Status: state.StatusSuccess}},
		{Database: state.Database{Name: "app_audit", Status: state.StatusFailure}},
	})

	want := []string{
		"app_prod_20261001T190000Z.dump",
		"app_prod_20261001T190000Z.dump.bak",
		"app_prod_20261014T190000Z.dump",
		"app_prod_20261015T190000Z.dump",
		"app_prod_20261015T190000Z.dump.partial",
		"notes.txt",
	}
	if got := listNames(t, prodDir); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := listNames(t, auditDir); len(got) != 2 {
		t.Fatalf("retention changed a database with a failed backup: %v", got)
	}
	if !strings.Contains(logs.String(), "Retention removed 3 old files of app_prod.") {
		t.Fatalf("got logs:\n%s", logs.String())
	}
}

func TestRetentionApplyMissingDirectory(t *testing.T) {
	cfg := &config.Config{
		Destination: config.Destination{RemotePath: t.TempDir()},
		Schedule:    config.Schedule{Frequency: config.FrequencyDaily, Timezone: "UTC"},
		Retention:   config.Retention{Daily: 2},
	}
	var logs bytes.Buffer
	r, err := NewRetention(cfg, config.Source{Name: "main_pg"}, localStore{}, "vm-app-01", slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r.Apply([]Result{{Database: state.Database{Name: "app_prod", Status: state.StatusSuccess}}})
	if !strings.Contains(logs.String(), "Retention of app_prod stopped. The backup result does not change.") {
		t.Fatalf("got logs:\n%s", logs.String())
	}
}

func TestRetentionUsesTheRulesOfTheSource(t *testing.T) {
	cfg := &config.Config{
		Destination: config.Destination{RemotePath: "/backups"},
		Schedule:    config.Schedule{Frequency: config.FrequencyDaily, Timezone: "UTC"},
		Retention:   config.Retention{Daily: 7, Weekly: 4, Monthly: 6},
	}
	cases := []struct {
		name   string
		source config.Source
		want   config.Retention
	}{
		{"global retention", config.Source{Name: "main_pg"}, cfg.Retention},
		{"retention of the source", config.Source{Name: "shop_mysql", Retention: &config.Retention{Daily: 14}}, config.Retention{Daily: 14}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := NewRetention(cfg, c.source, localStore{}, "vm-app-01", discardLogger())
			if err != nil {
				t.Fatal(err)
			}
			got := config.Retention{Daily: r.Rules.Daily, Weekly: r.Rules.Weekly, Monthly: r.Rules.Monthly}
			if got != c.want || r.Source != c.source.Name {
				t.Fatalf("got %+v for %s, want %+v for %s", got, r.Source, c.want, c.source.Name)
			}
		})
	}
}
