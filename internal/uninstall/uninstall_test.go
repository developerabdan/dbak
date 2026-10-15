package uninstall

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRemoveData(t *testing.T) {
	root := t.TempDir()
	d := Data{
		ConfigPath: filepath.Join(root, "etc", "dbak", "config.yaml"),
		StateDir:   filepath.Join(root, "var", "lib", "dbak"),
		LogDir:     filepath.Join(root, "var", "log", "dbak"),
	}
	for _, f := range []string{
		d.ConfigPath,
		filepath.Join(d.SecretsDir(), "db_password"),
		filepath.Join(d.SecretsDir(), "id_ed25519"),
		filepath.Join(d.SecretsDir(), "id_ed25519.pub"),
		filepath.Join(d.SecretsDir(), "known_hosts"),
		filepath.Join(d.StateDir, "state.json"),
		filepath.Join(d.StateDir, "dbak.lock"),
		filepath.Join(d.StateDir, "pgpass-123"),
		filepath.Join(d.LogDir, "dbak.log"),
		filepath.Join(d.LogDir, "dbak.log.1.gz"),
		filepath.Join(root, "keys", "custom_key"),
		filepath.Join(root, "keys", "other_key"),
		filepath.Join(root, "etc", "other.conf"),
	} {
		write(t, f)
	}
	write(t, filepath.Join(d.StateDir, "notes.txt"))
	if _, err := RemoveData(d); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{d.ConfigPath, d.SecretsDir(), filepath.Dir(d.ConfigPath), d.LogDir, filepath.Join(d.StateDir, "state.json")} {
		if exists(gone) {
			t.Errorf("%s still exists", gone)
		}
	}
	for _, kept := range []string{filepath.Join(d.StateDir, "notes.txt"), filepath.Join(root, "keys", "custom_key"), filepath.Join(root, "keys", "other_key"), filepath.Join(root, "etc", "other.conf")} {
		if !exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}
}
