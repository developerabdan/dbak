package uninstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/developerabdan/dbak/internal/app"
)

type Data struct {
	ConfigPath string
	StateDir   string
	LogDir     string
}

var secretNames = []string{"db_password", "id_ed25519", "id_ed25519.pub", "known_hosts", "telegram_token"}

func (d Data) SecretsDir() string {
	return filepath.Join(filepath.Dir(d.ConfigPath), "secrets")
}

func (d Data) Directories() []string {
	return []string{filepath.Dir(d.ConfigPath), d.StateDir, d.LogDir}
}

func (d Data) files() []string {
	var files []string
	for _, name := range secretNames {
		files = append(files, filepath.Join(d.SecretsDir(), name))
	}
	files = append(files, d.ConfigPath,
		filepath.Join(d.StateDir, app.StateFile),
		filepath.Join(d.StateDir, app.LockFile))
	for _, pattern := range []string{
		filepath.Join(d.SecretsDir(), "*", "db_password"),
		filepath.Join(d.StateDir, "pgpass-*"),
		filepath.Join(d.StateDir, "my-*.cnf"),
		filepath.Join(d.LogDir, "dbak.log*"),
	} {
		matches, _ := filepath.Glob(pattern)
		files = append(files, matches...)
	}
	return files
}

func RemoveData(d Data) ([]string, error) {
	var removed []string
	for _, file := range d.files() {
		err := os.Remove(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("cannot remove %s: %w", file, err)
		}
		removed = append(removed, file)
	}
	sourceDirs, _ := filepath.Glob(filepath.Join(d.SecretsDir(), "*"))
	dirs := append(sourceDirs, d.SecretsDir())
	for _, dir := range append(dirs, d.Directories()...) {
		err := os.Remove(dir)
		if err == nil {
			removed = append(removed, dir)
		}
	}
	return removed, nil
}
