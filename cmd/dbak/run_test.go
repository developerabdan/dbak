package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbak/internal/app"
	"dbak/internal/backup"
	"dbak/internal/lock"
	"dbak/internal/state"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSelectDatabases(t *testing.T) {
	configured := []string{"app_prod", "app_audit"}
	all, err := selectDatabases(configured, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("got %v %v", all, err)
	}
	one, err := selectDatabases(configured, "app_audit")
	if err != nil || len(one) != 1 || one[0] != "app_audit" {
		t.Fatalf("got %v %v", one, err)
	}
	if _, err := selectDatabases(configured, "other"); err == nil {
		t.Fatal("got no error for a database that is not in the config")
	}
}

func TestRunWithInvalidConfigGivesExitCode2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runBackup(context.Background(), &globalOptions{configPath: path, stateDir: dir}, "", discardLogger())
	if app.ExitCode(err) != app.ExitConfigInvalid {
		t.Fatalf("got exit code %d, want %d: %v", app.ExitCode(err), app.ExitConfigInvalid, err)
	}
}

func TestRunWhileLockedGivesExitCode7(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "testdata", "valid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := lock.Acquire(filepath.Join(dir, app.LockFile))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	var logs bytes.Buffer
	err = runBackup(context.Background(), &globalOptions{configPath: path, stateDir: dir}, "", slog.New(slog.NewTextHandler(&logs, nil)))
	if app.ExitCode(err) != app.ExitLocked {
		t.Fatalf("got exit code %d, want %d: %v", app.ExitCode(err), app.ExitLocked, err)
	}
	if !strings.Contains(logs.String(), "different dbak run") {
		t.Fatalf("the log does not show the lock: %s", logs.String())
	}
}

func TestRunError(t *testing.T) {
	ok := backup.Result{Database: state.Database{Status: state.StatusSuccess}}
	bad := backup.Result{Database: state.Database{Status: state.StatusFailure}, FailureKind: backup.FailureDump}
	if err := runError([]backup.Result{ok}); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	err := runError([]backup.Result{ok, bad})
	if app.ExitCode(err) != app.ExitPartial || !strings.Contains(err.Error(), "1 of 2 databases") {
		t.Fatalf("got %v", err)
	}
}

func TestSentence(t *testing.T) {
	if got := sentence("the file is missing. Make it"); got != "The file is missing. Make it." {
		t.Fatalf("got %q", got)
	}
}
