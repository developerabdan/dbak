package mysql

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/developerabdan/dbak/internal/engine"
)

func testEngine(t *testing.T) *MySQL {
	return New(engine.Options{
		Host:     "10.0.0.10",
		Port:     3306,
		Username: "backup_ro",
		Password: "p\"a\\ss#word",
		SSLMode:  "PREFERRED",
		TempDir:  t.TempDir(),
	})
}

var (
	mysql8Client = engine.ClientInfo{Major: 8}
	mariaClient  = engine.ClientInfo{Major: 10, MariaDB: true}
)

func TestDumpArgs(t *testing.T) {
	cases := []struct {
		name        string
		server      engine.ServerInfo
		client      engine.ClientInfo
		wantGTID    bool
		wantColumns bool
	}{
		{"mysql server without gtid", engine.ServerInfo{Flavor: engine.FlavorMySQL}, mysql8Client, false, false},
		{"mysql server with gtid", engine.ServerInfo{Flavor: engine.FlavorMySQL, GTIDEnabled: true}, mysql8Client, true, false},
		{"mysql server with gtid and mariadb client", engine.ServerInfo{Flavor: engine.FlavorMySQL, GTIDEnabled: true}, mariaClient, false, false},
		{"mariadb server and mariadb client", engine.ServerInfo{Flavor: engine.FlavorMariaDB, GTIDEnabled: true}, mariaClient, false, false},
		{"mariadb server and mysql 8 client", engine.ServerInfo{Flavor: engine.FlavorMariaDB}, mysql8Client, false, true},
		{"mariadb server and mysql 5.7 client", engine.ServerInfo{Flavor: engine.FlavorMariaDB}, engine.ClientInfo{Major: 5}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := testEngine(t)
			m.server = c.server
			args := m.dumpArgs("/tmp/x.cnf", "app_prod", c.client)
			if args[0] != "--defaults-file=/tmp/x.cnf" {
				t.Fatalf("got first argument %s, want the option file", args[0])
			}
			if got := slices.Contains(args, "--set-gtid-purged=OFF"); got != c.wantGTID {
				t.Fatalf("got gtid flag %v, want %v", got, c.wantGTID)
			}
			if got := slices.Contains(args, "--column-statistics=0"); got != c.wantColumns {
				t.Fatalf("got column statistics flag %v, want %v", got, c.wantColumns)
			}
			if !slices.Equal(args[len(args)-2:], []string{"--databases", "app_prod"}) {
				t.Fatalf("got %v, want --databases app_prod at the end", args)
			}
			for _, flag := range []string{"--single-transaction", "--quick", "--routines", "--triggers", "--events", "--no-tablespaces"} {
				if !slices.Contains(args, flag) {
					t.Fatalf("flag %s is missing", flag)
				}
			}
		})
	}
}

func TestOptionFileForMySQLClient(t *testing.T) {
	got := testEngine(t).optionFile(false)
	want := "[client]\nhost=\"10.0.0.10\"\nport=3306\nuser=\"backup_ro\"\npassword=\"p\\\"a\\\\ss#word\"\nssl-mode=PREFERRED\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOptionFileForMariaDBClient(t *testing.T) {
	m := testEngine(t)
	cases := map[string][]string{
		"disabled":        {"skip-ssl"},
		"preferred":       {"skip-ssl-verify-server-cert"},
		"required":        {"ssl", "skip-ssl-verify-server-cert"},
		"verify_identity": {"ssl", "ssl-verify-server-cert"},
	}
	for mode, want := range cases {
		t.Run("ssl mode "+mode, func(t *testing.T) {
			m.opts.SSLMode = mode
			content := m.optionFile(true)
			if strings.Contains(content, "ssl-mode") {
				t.Fatal("the MariaDB client does not know ssl-mode")
			}
			for _, line := range want {
				if !strings.Contains(content, "\n"+line+"\n") {
					t.Fatalf("line %s is missing in %q", line, content)
				}
			}
		})
	}
}

func TestDumpCommandUsesSecretFile(t *testing.T) {
	t.Setenv("MYSQL_PWD", "leak")
	m := testEngine(t)
	m.opts.Binary = "/nonexistent/mysqldump"
	m.server = engine.ServerInfo{Flavor: engine.FlavorMySQL}
	dump, err := m.DumpCommand(context.Background(), "app_prod")
	if err != nil {
		t.Fatal(err)
	}
	optionFile := strings.TrimPrefix(dump.Cmd.Args[1], "--defaults-file=")
	info, err := os.Stat(optionFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("got mode %v, want 0600", info.Mode().Perm())
	}
	for _, arg := range dump.Cmd.Args {
		if strings.Contains(arg, "ss#word") {
			t.Fatal("the password is in the process arguments")
		}
	}
	for _, entry := range dump.Cmd.Env {
		if strings.HasPrefix(entry, "MYSQL_PWD=") {
			t.Fatal("MYSQL_PWD is in the child environment")
		}
	}
	if err := dump.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(optionFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the option file exists after cleanup")
	}
}

func TestParseServerVersion(t *testing.T) {
	cases := []struct {
		version string
		flavor  string
		major   int
	}{
		{"8.0.36", engine.FlavorMySQL, 8},
		{"5.7.44-log", engine.FlavorMySQL, 5},
		{"10.11.6-MariaDB-0+deb12u1", engine.FlavorMariaDB, 10},
		{"11.4.2-MariaDB", engine.FlavorMariaDB, 11},
	}
	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			info := ParseServerVersion(c.version)
			if info.Flavor != c.flavor || info.Major != c.major {
				t.Fatalf("got %+v", info)
			}
		})
	}
}

func TestIsMariaDBClient(t *testing.T) {
	if IsMariaDBClient("mysqldump  Ver 8.0.36 for Linux on x86_64 (MySQL Community Server - GPL)") {
		t.Fatal("got MariaDB for a MySQL client")
	}
	if !IsMariaDBClient("mysqldump  Ver 10.19 Distrib 10.11.6-MariaDB, for debian-linux-gnu (x86_64)") {
		t.Fatal("got MySQL for a MariaDB client")
	}
}

func TestWrapOutputCompressesWithZstd(t *testing.T) {
	var buf bytes.Buffer
	w, err := testEngine(t).WrapOutput(&buf)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("INSERT INTO t VALUES (1);\n", 1000)
	if _, err := io.WriteString(w, payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := zstd.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatal("the decompressed data is different")
	}
}

func TestProbeUnreachableHost(t *testing.T) {
	m := New(engine.Options{Host: "127.0.0.1", Port: 1, Username: "u", Password: "p", SSLMode: "disabled"})
	_, err := m.Probe(context.Background(), "")
	var connErr *engine.ConnectError
	if !errors.As(err, &connErr) || !strings.Contains(err.Error(), "cannot connect") {
		t.Fatalf("got %v, want a connect error", err)
	}
}

func TestDumpCommandReadsServerInfoFirst(t *testing.T) {
	m := New(engine.Options{Host: "127.0.0.1", Port: 1, Username: "u", Password: "p", SSLMode: "disabled", TempDir: t.TempDir()})
	_, err := m.DumpCommand(context.Background(), "app_prod")
	var connErr *engine.ConnectError
	if !errors.As(err, &connErr) {
		t.Fatalf("got %v, want a connect error before the dump", err)
	}
}
