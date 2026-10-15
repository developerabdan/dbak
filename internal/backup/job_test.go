package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/lock"
	"github.com/developerabdan/dbak/internal/notify"
	"github.com/developerabdan/dbak/internal/state"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSelectDatabases(t *testing.T) {
	configured := []string{"app_prod", "app_audit"}
	all, err := SelectDatabases(configured, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("got %v %v", all, err)
	}
	one, err := SelectDatabases(configured, "app_audit")
	if err != nil || len(one) != 1 || one[0] != "app_audit" {
		t.Fatalf("got %v %v", one, err)
	}
	if _, err := SelectDatabases(configured, "other"); err == nil {
		t.Fatal("got no error for a database that is not in the config")
	}
}

func TestRunWithInvalidConfigGivesExitCode2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RunJob(context.Background(), JobOptions{ConfigPath: path, StateDir: dir, Logger: discardLogger()})
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
	_, err = RunJob(context.Background(), JobOptions{ConfigPath: path, StateDir: dir, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if app.ExitCode(err) != app.ExitLocked {
		t.Fatalf("got exit code %d, want %d: %v", app.ExitCode(err), app.ExitLocked, err)
	}
	if !strings.Contains(logs.String(), "different dbak run") {
		t.Fatalf("the log does not show the lock: %s", logs.String())
	}
}

func TestRunError(t *testing.T) {
	ok := Result{Database: state.Database{Status: state.StatusSuccess}}
	bad := Result{Database: state.Database{Status: state.StatusFailure}, FailureKind: FailureDump}
	if err := runError([]Result{ok}); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	err := runError([]Result{ok, bad})
	if app.ExitCode(err) != app.ExitPartial || !strings.Contains(err.Error(), "1 of 2 databases") {
		t.Fatalf("got %v", err)
	}
}

func TestEarlyFailureSendsNotifications(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var payload notify.Payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/hook" {
			json.NewDecoder(r.Body).Decode(&payload)
		}
	}))
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	dir := t.TempDir()
	passwordFile := filepath.Join(dir, "db_password")
	if err := os.WriteFile(passwordFile, []byte("test-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := strings.NewReplacer(
		"host: 10.0.0.10", "host: 127.0.0.1",
		"port: 5432", "port: "+strconv.Itoa(closedPort),
		"/etc/dbak/secrets/db_password", passwordFile,
		`webhook_url: ""`, `webhook_url: "`+server.URL+`/hook"`,
		`healthcheck_url: ""`, `healthcheck_url: "`+server.URL+`/ping"`,
	).Replace(readValidConfig(t))
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := RunJob(context.Background(), JobOptions{ConfigPath: path, StateDir: dir, Logger: discardLogger()})
	if app.ExitCode(err) != app.ExitDatabase {
		t.Fatalf("got exit code %d, want %d: %v", app.ExitCode(err), app.ExitDatabase, err)
	}
	if record.Status != state.StatusFailure || len(record.Databases) != 2 {
		t.Fatalf("got record %+v", record)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(paths, []string{"/ping/start", "/ping/fail", "/hook"}) {
		t.Fatalf("got requests %v", paths)
	}
	if payload.Status != state.StatusFailure || len(payload.Databases) != 2 || payload.Databases[0].Error == "" {
		t.Fatalf("got payload %+v", payload)
	}
	saved, err := state.Load(filepath.Join(dir, app.StateFile))
	if err != nil || len(saved.Runs) != 1 || saved.Runs[0].Status != state.StatusFailure {
		t.Fatalf("got state %+v %v", saved, err)
	}
}

func readValidConfig(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "testdata", "valid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
