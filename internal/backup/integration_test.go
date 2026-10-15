//go:build integration

package backup_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/backup"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/state"
)

const (
	sftpHost       = "127.0.0.1"
	sftpPort       = 52222
	sftpRemotePath = "/upload"
)

type target struct {
	name      string
	engine    string
	port      int
	sslMode   string
	binEnv    string
	databases []string
	verify    func(t *testing.T, data []byte)
}

func integrationDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "integration"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeConfig(t *testing.T, tg target, knownHosts string) backup.JobOptions {
	t.Helper()
	fixtures := integrationDir(t)
	cfg := &config.Config{
		Version: config.CurrentVersion,
		Source: config.Source{
			Engine:       tg.engine,
			Host:         "127.0.0.1",
			Port:         tg.port,
			Username:     "backup_ro",
			PasswordFile: filepath.Join(fixtures, "db_password"),
			SSLMode:      tg.sslMode,
			Databases:    tg.databases,
		},
		Destination: config.Destination{
			Type:           "sftp",
			Host:           sftpHost,
			Port:           sftpPort,
			Username:       "dbak",
			PrivateKeyFile: filepath.Join(fixtures, "ssh", "id_ed25519"),
			KnownHostsFile: knownHosts,
			RemotePath:     sftpRemotePath,
		},
		Schedule:  config.Schedule{Frequency: config.FrequencyDaily, Time: "02:00", Timezone: "UTC"},
		Retention: config.Retention{Daily: 7, Weekly: 4, Monthly: 6},
		Notify:    config.Notify{When: config.NotifyFailure},
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	return backup.JobOptions{ConfigPath: configPath, StateDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func pinHostKey(t *testing.T) string {
	t.Helper()
	key, err := sftp.FetchHostKey(sftpHost, sftpPort, 10*time.Second)
	if err != nil {
		t.Skipf("the SFTP test server is not available: %v", err)
	}
	file := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(file, []byte(sftp.KnownHostsLine(sftpHost, sftpPort, key)), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func usePath(t *testing.T, envName string) {
	t.Helper()
	if dir := os.Getenv(envName); dir != "" {
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
}

func waitForDatabase(t *testing.T, opts backup.JobOptions) {
	t.Helper()
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	password, err := config.ReadSecret(cfg.Source.PasswordFile)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := backup.NewEngine(cfg.Source, password, opts.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := eng.Probe(ctx, cfg.Source.Databases[0])
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Skipf("the database server on port %d is not available: %v", cfg.Source.Port, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func readRemote(t *testing.T, knownHosts, remoteFile string) []byte {
	t.Helper()
	client := dialTestSFTP(t, knownHosts)
	defer client.Close()
	r, err := client.Open(remoteFile)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func remoteNames(t *testing.T, knownHosts, dir string) []string {
	t.Helper()
	client := dialTestSFTP(t, knownHosts)
	defer client.Close()
	entries, err := client.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func dialTestSFTP(t *testing.T, knownHosts string) *sftp.Client {
	t.Helper()
	client, err := sftp.Dial(sftp.Config{
		Host:           sftpHost,
		Port:           sftpPort,
		Username:       "dbak",
		PrivateKeyFile: filepath.Join(integrationDir(t), "ssh", "id_ed25519"),
		KnownHostsFile: knownHosts,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func verifyPostgres(t *testing.T, data []byte) {
	if !bytes.HasPrefix(data, []byte("PGDMP")) {
		t.Fatal("the backup is not a pg_dump custom format file")
	}
}

func verifyMySQL(t *testing.T, data []byte) {
	r, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	sql, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CREATE TABLE `customers`", "'alpha'", "'gamma'"} {
		if !strings.Contains(string(sql), want) {
			t.Fatalf("the dump does not contain %s", want)
		}
	}
}

func targets() []target {
	return []target{
		{"postgres 16", config.EnginePostgres, 55416, "disable", "DBAK_IT_PG_BIN", []string{"app_prod"}, verifyPostgres},
		{"postgres 13", config.EnginePostgres, 55413, "disable", "DBAK_IT_PG_BIN", []string{"app_prod"}, verifyPostgres},
		{"mysql 8.0", config.EngineMySQL, 53380, "preferred", "DBAK_IT_MYSQL_BIN", []string{"app_prod"}, verifyMySQL},
		{"mariadb 10.11", config.EngineMySQL, 53310, "preferred", "DBAK_IT_MARIADB_BIN", []string{"app_prod"}, verifyMySQL},
	}
}

func TestIntegrationBackupEndToEnd(t *testing.T) {
	knownHosts := pinHostKey(t)
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets() {
		t.Run(tg.name, func(t *testing.T) {
			usePath(t, tg.binEnv)
			opts := writeConfig(t, tg, knownHosts)
			waitForDatabase(t, opts)
			var logs bytes.Buffer
			opts.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			_, err := backup.RunJob(context.Background(), opts)
			if err != nil {
				t.Fatalf("the run failed: %v\n%s", err, logs.String())
			}
			if strings.Contains(logs.String(), "Te:st") {
				t.Fatal("the log contains the password")
			}
			history, err := state.Load(filepath.Join(opts.StateDir, app.StateFile))
			if err != nil {
				t.Fatal(err)
			}
			result := history.Runs[len(history.Runs)-1].Databases[0]
			if result.Status != state.StatusSuccess || result.SizeBytes == 0 {
				t.Fatalf("unexpected result: %+v", result)
			}
			if !strings.HasPrefix(result.RemotePath, path.Join(sftpRemotePath, hostname, "app_prod")+"/") {
				t.Fatalf("unexpected remote path %s", result.RemotePath)
			}
			tg.verify(t, readRemote(t, knownHosts, result.RemotePath))
			matches, _ := filepath.Glob(filepath.Join(opts.StateDir, "*pgpass*"))
			cnfs, _ := filepath.Glob(filepath.Join(opts.StateDir, "my-*.cnf"))
			if len(matches)+len(cnfs) != 0 {
				t.Fatalf("temporary credential files remain: %v %v", matches, cnfs)
			}
		})
	}
}

func TestIntegrationMissingPrivilegeFailsWithoutFiles(t *testing.T) {
	knownHosts := pinHostKey(t)
	tg := target{"postgres 13 restricted", config.EnginePostgres, 55413, "disable", "DBAK_IT_PG_BIN", []string{"app_restricted"}, nil}
	usePath(t, tg.binEnv)
	opts := writeConfig(t, tg, knownHosts)
	waitForDatabase(t, opts)
	_, err := backup.RunJob(context.Background(), opts)
	if app.ExitCode(err) != app.ExitDumpFailed {
		t.Fatalf("got exit code %d, want %d: %v", app.ExitCode(err), app.ExitDumpFailed, err)
	}
	history, err := state.Load(filepath.Join(opts.StateDir, app.StateFile))
	if err != nil {
		t.Fatal(err)
	}
	result := history.Runs[0].Databases[0]
	if !strings.Contains(result.Error, "audit_log") {
		t.Fatalf("the error does not name the table: %s", result.Error)
	}
	hostname, _ := os.Hostname()
	for _, name := range remoteNames(t, knownHosts, path.Join(sftpRemotePath, hostname, "app_restricted")) {
		t.Fatalf("a remote file exists after a failed dump: %s", name)
	}
}

func TestIntegrationChangedHostKeyIsRejected(t *testing.T) {
	pinHostKey(t)
	fixtures := integrationDir(t)
	pub, err := os.ReadFile(filepath.Join(fixtures, "ssh", "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(pub))
	wrong := filepath.Join(t.TempDir(), "known_hosts")
	line := fmt.Sprintf("[%s]:%d %s %s\n", sftpHost, sftpPort, fields[0], fields[1])
	if err := os.WriteFile(wrong, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	tg := targets()[0]
	usePath(t, tg.binEnv)
	opts := writeConfig(t, tg, wrong)
	waitForDatabase(t, opts)
	_, err = backup.RunJob(context.Background(), opts)
	if app.ExitCode(err) != app.ExitUploadFailed || !errors.Is(err, sftp.ErrHostKeyChanged) {
		t.Fatalf("got %v with exit code %d, want a changed host key error", err, app.ExitCode(err))
	}
}
