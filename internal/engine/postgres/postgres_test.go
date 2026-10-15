package postgres

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/developerabdan/dbak/internal/engine"
)

func testOptions(t *testing.T) engine.Options {
	return engine.Options{
		Host:     "10.0.0.10",
		Port:     5432,
		Username: "backup_ro",
		Password: `pa:ss\word`,
		SSLMode:  "prefer",
		TempDir:  t.TempDir(),
	}
}

func TestDumpCommandArgs(t *testing.T) {
	p := New(testOptions(t))
	dump, err := p.DumpCommand(context.Background(), "app_prod")
	if err != nil {
		t.Fatal(err)
	}
	defer dump.Cleanup()
	want := []string{DefaultBinary, "--format=custom", "--compress=6", "--no-password",
		"--host", "10.0.0.10", "--port", "5432", "--username", "backup_ro", "--dbname", "app_prod"}
	if !slices.Equal(dump.Cmd.Args, want) {
		t.Fatalf("got %v, want %v", dump.Cmd.Args, want)
	}
	for _, arg := range dump.Cmd.Args {
		if strings.Contains(arg, `pa:ss`) {
			t.Fatal("the password is in the process arguments")
		}
	}
}

func TestDumpCommandPasswordFile(t *testing.T) {
	t.Setenv("PGPASSWORD", "leak")
	p := New(testOptions(t))
	dump, err := p.DumpCommand(context.Background(), "app_prod")
	if err != nil {
		t.Fatal(err)
	}
	var passFile string
	for _, entry := range dump.Cmd.Env {
		if value, ok := strings.CutPrefix(entry, "PGPASSFILE="); ok {
			passFile = value
		}
		if strings.HasPrefix(entry, "PGPASSWORD=") {
			t.Fatal("PGPASSWORD from the parent environment is in the child environment")
		}
	}
	if !slices.Contains(dump.Cmd.Env, "PGSSLMODE=prefer") {
		t.Fatal("PGSSLMODE is not in the environment")
	}
	info, err := os.Stat(passFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("got mode %v, want 0600", info.Mode().Perm())
	}
	content, err := os.ReadFile(passFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "10.0.0.10:5432:*:backup_ro:pa\\:ss\\\\word\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if err := dump.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(passFile); !os.IsNotExist(err) {
		t.Fatal("the password file exists after cleanup")
	}
}

func TestConnStringQuotes(t *testing.T) {
	p := New(engine.Options{Host: "db host", Port: 5432, SSLMode: "require"})
	got := p.connString("it's")
	want := `host='db host' port=5432 dbname='it\'s' sslmode='require'`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if strings.Contains(got, "password") {
		t.Fatal("the connection string contains a password")
	}
}

func TestProbeUnreachableHost(t *testing.T) {
	p := New(engine.Options{Host: "127.0.0.1", Port: 1, Username: "u", Password: "p", SSLMode: "disable"})
	_, err := p.Probe(context.Background(), "postgres")
	var connErr *engine.ConnectError
	if !errors.As(err, &connErr) || !strings.Contains(err.Error(), "cannot connect") {
		t.Fatalf("got %v, want a connect error", err)
	}
}
