package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/developerabdan/dbak/internal/backup"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/encrypt"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/system"
)

func (h *harness) prepareRestore(t *testing.T) string {
	t.Helper()
	h.installOnePostgresSource(t)
	h.env.OpenBackups = func(cfg sftp.Config) (BackupStore, error) {
		cfg.Timeout = 5 * time.Second
		return sftp.Dial(cfg)
	}
	h.env.RestoreDir = filepath.Join(t.TempDir(), "home", "backups")
	h.env.RestoreOwner = system.CurrentOwner()
	return h.env.RestoreDir
}

func (h *harness) putBackup(t *testing.T, database, name string, data []byte) {
	t.Helper()
	dir := filepath.Join(h.remoteRoot, h.env.Hostname, "postgres", database)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sealFor(t *testing.T, public, text string) []byte {
	t.Helper()
	recipients, err := encrypt.ParseRecipients([]string{public})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w, err := encrypt.NewWriter(&out, recipients)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestRestoreWritesTheSelectedBackup(t *testing.T) {
	h := newHarness(t)
	dir := h.prepareRestore(t)
	h.putBackup(t, "app_prod", "app_prod_20261014T190000Z.dump", []byte("old dump"))
	h.putBackup(t, "app_prod", "app_prod_20261015T190000Z.dump", []byte("new dump"))
	h.putBackup(t, "app_audit", "app_audit_20261015T180000Z.dump", []byte("audit dump"))
	h.putBackup(t, "app_prod", "app_prod_20261015T200000Z.dump.partial", []byte("partial"))

	err := h.runCommand(t, Restore,
		answer{match: "Select the backup to restore", value: "2"},
		answer{match: "Directory for the backup file"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "app_prod_20261014T190000Z.dump"))
	if err != nil || string(got) != "old dump" {
		t.Fatalf("got %q and %v", got, err)
	}
	out := h.ui.out.String()
	for _, want := range []string{"Source: postgres (postgres)", "dbak wrote the backup to " + dir} {
		if !strings.Contains(out, want) {
			t.Fatalf("the output has no %q:\n%s", want, out)
		}
	}
}

func TestBackupLabelsAreNewestFirst(t *testing.T) {
	h := newHarness(t)
	h.prepareRestore(t)
	h.putBackup(t, "app_prod", "app_prod_20261014T190000Z.dump", []byte("old dump"))
	h.putBackup(t, "app_audit", "app_audit_20261015T180000Z.dump.age", []byte("sealed"))
	cfg, err := existingConfig(h.env)
	if err != nil {
		t.Fatal(err)
	}
	client, err := h.env.OpenBackups(backup.SFTPConfig(cfg.Destination))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	entries, err := sourceBackups(client, cfg, h.env.Hostname, cfg.Sources[0])
	if err != nil {
		t.Fatal(err)
	}
	loc := scheduleLocation(cfg)
	want := []string{
		"2026-10-16 01:00 WIB  app_audit  6 B  encrypted",
		"2026-10-15 02:00 WIB  app_prod  8 B",
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for i, e := range entries {
		if got := backupLabel(e, loc); got != want[i] {
			t.Fatalf("label %d: got %q, want %q", i, got, want[i])
		}
	}
}

func TestRestoreDecryptsWithThePastedKey(t *testing.T) {
	h := newHarness(t)
	dir := h.prepareRestore(t)
	pair, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	h.putBackup(t, "app_prod", "app_prod_20261015T190000Z.dump.age", sealFor(t, pair.Public, "secret dump"))

	err = h.runCommand(t, Restore,
		answer{match: "Select the backup to restore", value: "0"},
		answer{match: "Directory for the backup file", value: dir},
		answer{match: "Paste the age private key", value: other.Private},
		answer{match: "Paste the age private key", value: pair.Private},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "app_prod_20261015T190000Z.dump"))
	if err != nil || string(got) != "secret dump" {
		t.Fatalf("got %q and %v", got, err)
	}
	out := h.ui.out.String()
	if !strings.Contains(out, "ERROR: The private key cannot decrypt this backup.") {
		t.Fatalf("the output has no error for the wrong key:\n%s", out)
	}
	if strings.Contains(out, pair.Private) || strings.Contains(out, other.Private) {
		t.Fatal("the output contains a private key")
	}
}

func TestRestoreWithoutBackups(t *testing.T) {
	h := newHarness(t)
	h.prepareRestore(t)
	err := h.runCommand(t, Restore)
	if err == nil || !strings.Contains(err.Error(), "has no backups of the source postgres") {
		t.Fatalf("got %v", err)
	}
}

func TestSelectRestoreSourceWithTwoSources(t *testing.T) {
	ui := newFakeUI(t, answer{match: "Select the source", value: "shop"})
	cfg := &config.Config{Sources: []config.Source{
		{Name: "main", Engine: config.EnginePostgres, Host: "10.0.0.1", Port: 5432},
		{Name: "shop", Engine: config.EngineMySQL, Host: "10.0.0.2", Port: 3306},
	}}
	src, err := selectRestoreSource(ui, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if src.Name != "shop" {
		t.Fatalf("got %s, want shop", src.Name)
	}
	ui.done()
}
