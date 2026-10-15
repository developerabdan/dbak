package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/schedule"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/system"
)

type fakeEngine struct {
	server     engine.ServerInfo
	client     engine.ClientInfo
	probeErr   error
	privileges map[string]engine.PrivilegeReport
	schemaErr  map[string]error
}

func (f *fakeEngine) Name() string          { return "fake" }
func (f *fakeEngine) FileExtension() string { return "dump" }
func (f *fakeEngine) Probe(context.Context, string) (engine.ServerInfo, error) {
	return f.server, f.probeErr
}
func (f *fakeEngine) DumpCommand(context.Context, string) (*engine.Dump, error) {
	return nil, errors.New("not used")
}
func (f *fakeEngine) WrapOutput(w io.Writer) (io.WriteCloser, error) {
	return nil, errors.New("not used")
}
func (f *fakeEngine) ProbeServer(context.Context) (engine.ServerInfo, error) {
	return f.server, f.probeErr
}
func (f *fakeEngine) ListDatabases(context.Context) ([]string, error) { return nil, nil }
func (f *fakeEngine) ClientVersion(context.Context) (engine.ClientInfo, error) {
	return f.client, nil
}
func (f *fakeEngine) SchemaCheck(_ context.Context, db string) error { return f.schemaErr[db] }
func (f *fakeEngine) PrivilegeCheck(_ context.Context, db string) (engine.PrivilegeReport, error) {
	return f.privileges[db], nil
}

type fakeSFTP struct{ writeErr error }

func (f *fakeSFTP) WriteCheck(string) error { return f.writeErr }
func (f *fakeSFTP) Close() error            { return nil }

type fixture struct {
	env       *Env
	eng       *fakeEngine
	sftp      *fakeSFTP
	dialErr   error
	installed schedule.Installed
	clock     string
	clockErr  error
	dir       string
	engines   map[string]*fakeEngine
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	secrets := filepath.Join(dir, "etc", "secrets")
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"db_password", "id_ed25519", "known_hosts"} {
		if err := os.WriteFile(filepath.Join(secrets, name), []byte("test-value\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Version: config.CurrentVersion,
		Sources: []config.Source{{
			Name: "main_pg", Engine: config.EnginePostgres, Host: "10.0.0.10", Port: 5432, Username: "backup_ro",
			PasswordFile: filepath.Join(secrets, "db_password"), SSLMode: "prefer", Databases: []string{"app_prod", "app_audit"},
		}},
		Destination: config.Destination{
			Type: "sftp", Host: "backup.example.com", Port: 22, Username: "dbak",
			PrivateKeyFile: filepath.Join(secrets, "id_ed25519"), KnownHostsFile: filepath.Join(secrets, "known_hosts"), RemotePath: "/backups",
		},
		Schedule:  config.Schedule{Frequency: config.FrequencyDaily, Time: "02:00", Timezone: "UTC", RandomizedDelay: "0"},
		Retention: config.Retention{Daily: 7},
		Notify:    config.Notify{When: config.NotifyFailure},
	}
	configPath := filepath.Join(dir, "etc", "config.yaml")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	owner := system.CurrentOwner()
	f := &fixture{
		dir: dir,
		eng: &fakeEngine{
			server:     engine.ServerInfo{Flavor: engine.FlavorPostgres, Version: "16.4", Major: 16},
			client:     engine.ClientInfo{Text: "pg_dump (PostgreSQL) 16.4", Major: 16},
			privileges: map[string]engine.PrivilegeReport{},
			schemaErr:  map[string]error{},
		},
		sftp:      &fakeSFTP{},
		installed: schedule.Installed{Mode: schedule.ModeSystemd, Enabled: "enabled", Active: "active", Expression: "*-*-* 02:00:00 UTC"},
		clock:     "yes\n",
	}
	f.env = &Env{
		ConfigPath: configPath,
		StateDir:   state,
		Hostname:   "vm-app-01",
		Owner:      &owner,
		NewEngine: func(source config.Source, _ string) (engine.Full, error) {
			if other, ok := f.engines[source.Name]; ok {
				return other, nil
			}
			return f.eng, nil
		},
		DialSFTP: func(sftp.Config) (SFTPClient, error) {
			if f.dialErr != nil {
				return nil, f.dialErr
			}
			return f.sftp, nil
		},
		Schedule: func(context.Context) (schedule.Installed, error) { return f.installed, nil },
		Run: func(context.Context, string, ...string) (string, error) {
			return f.clock, f.clockErr
		},
	}
	return f
}

func levels(results []Result) string {
	var parts []string
	for _, r := range results {
		parts = append(parts, fmt.Sprintf("%s=%s", r.Name, r.Level))
	}
	return strings.Join(parts, " ")
}

func find(t *testing.T, results []Result, name string) Result {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no result for %s in %s", name, levels(results))
	return Result{}
}

func TestAllChecksPass(t *testing.T) {
	f := newFixture(t)
	results := Run(context.Background(), f.env)
	want := "Config=OK Permissions=OK Client tool main_pg=OK Database main_pg=OK Dump check main_pg/app_prod=OK Dump check main_pg/app_audit=OK SFTP=OK Schedule=OK Clock=OK"
	if got := levels(results); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if Failed(results) != 0 {
		t.Fatal("got failures")
	}
	for _, r := range results {
		if strings.Contains(r.Message, "test-value") || strings.Contains(r.Fix, "test-value") {
			t.Fatalf("the output contains a secret: %+v", r)
		}
	}
}

func TestInvalidConfigStops(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.env.ConfigPath, []byte("version: 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	results := Run(context.Background(), f.env)
	if len(results) != 1 || results[0].Level != Fail || results[0].Fix == "" {
		t.Fatalf("got %+v", results)
	}
}

func TestLoosePermissions(t *testing.T) {
	f := newFixture(t)
	password := filepath.Join(f.dir, "etc", "secrets", "db_password")
	if err := os.Chmod(password, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(f.dir, "etc", "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	results := Run(context.Background(), f.env)
	var fixes []string
	for _, r := range results {
		if r.Name == "Permissions" {
			if r.Level != Fail {
				t.Fatalf("got %+v", r)
			}
			fixes = append(fixes, r.Fix)
		}
	}
	want := []string{"Run: sudo chmod 0700 " + filepath.Join(f.dir, "etc", "secrets"), "Run: sudo chmod 0600 " + password}
	if len(fixes) != 2 || fixes[0] != want[0] || fixes[1] != want[1] {
		t.Fatalf("got fixes %q, want %q", fixes, want)
	}
}

func TestWrongOwner(t *testing.T) {
	f := newFixture(t)
	other := system.Owner{UID: f.env.Owner.UID + 1, GID: f.env.Owner.GID}
	f.env.Owner = &other
	r := find(t, Run(context.Background(), f.env), "Permissions")
	if r.Level != Fail || !strings.Contains(r.Fix, "sudo chown dbak:dbak") {
		t.Fatalf("got %+v", r)
	}
}

func TestMissingServiceUser(t *testing.T) {
	f := newFixture(t)
	f.env.Owner = nil
	r := find(t, Run(context.Background(), f.env), "Permissions")
	if r.Level != Fail || !strings.Contains(r.Message, "The user dbak does not exist") {
		t.Fatalf("got %+v", r)
	}
}

func TestDatabaseFailureSkipsDumpChecks(t *testing.T) {
	f := newFixture(t)
	f.eng.probeErr = &engine.ConnectError{Message: "cannot connect to PostgreSQL at 10.0.0.10:5432"}
	results := Run(context.Background(), f.env)
	want := "Config=OK Permissions=OK Client tool main_pg=OK Database main_pg=FAIL SFTP=OK Schedule=OK Clock=OK"
	if got := levels(results); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestOldClient(t *testing.T) {
	f := newFixture(t)
	f.eng.client = engine.ClientInfo{Text: "pg_dump (PostgreSQL) 14.2", Major: 14}
	r := find(t, Run(context.Background(), f.env), "Client tool main_pg")
	if r.Level != Fail || !strings.Contains(r.Fix, "PostgreSQL 16 client tools") || !strings.Contains(r.Fix, "bin_dir of the source main_pg") {
		t.Fatalf("got %+v", r)
	}
}

func TestDumpCheckProblems(t *testing.T) {
	f := newFixture(t)
	f.eng.privileges["app_audit"] = engine.PrivilegeReport{Problems: []string{`"public"."audit_log"`}, FixSQL: "GRANT SELECT ON ALL TABLES\nIN SCHEMA public TO backup_ro;"}
	f.eng.schemaErr["app_prod"] = errors.New("pg_dump: error: permission denied for schema private")
	results := Run(context.Background(), f.env)
	prod := find(t, results, "Dump check main_pg/app_prod")
	audit := find(t, results, "Dump check main_pg/app_audit")
	if prod.Level != Fail || !strings.Contains(prod.Message, "permission denied for schema private") {
		t.Fatalf("got %+v", prod)
	}
	if audit.Level != Fail || !strings.Contains(audit.Message, `"public"."audit_log"`) || !strings.Contains(audit.Fix, "GRANT SELECT ON ALL TABLES IN SCHEMA public TO backup_ro;") {
		t.Fatalf("got %+v", audit)
	}
}

func TestSFTPProblems(t *testing.T) {
	f := newFixture(t)
	f.dialErr = sftp.ErrHostKeyChanged
	r := find(t, Run(context.Background(), f.env), "SFTP")
	if r.Level != Fail || !strings.Contains(r.Fix, "Find the cause") {
		t.Fatalf("got %+v", r)
	}
	f.dialErr = nil
	f.sftp.writeErr = errors.New("cannot write to /backups/vm-app-01")
	r = find(t, Run(context.Background(), f.env), "SFTP")
	if r.Level != Fail || !strings.Contains(r.Fix, "write permission") {
		t.Fatalf("got %+v", r)
	}
}

func TestSchedule(t *testing.T) {
	cases := []struct {
		name      string
		installed schedule.Installed
		cron      bool
		want      Level
	}{
		{"timer inactive", schedule.Installed{Mode: schedule.ModeSystemd, Enabled: "disabled", Active: "inactive"}, false, Fail},
		{"cron with a process", schedule.Installed{Mode: schedule.ModeCron, Expression: "0 2 * * *"}, true, OK},
		{"cron without a process", schedule.Installed{Mode: schedule.ModeCron, Expression: "0 2 * * *"}, false, Warn},
		{"nothing installed", schedule.Installed{}, false, Fail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.installed = c.installed
			f.env.CronRunning = func() bool { return c.cron }
			if r := find(t, Run(context.Background(), f.env), "Schedule"); r.Level != c.want {
				t.Fatalf("got %+v", r)
			}
		})
	}
}

func TestClock(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
		want Level
	}{
		{"synchronized", "yes\n", nil, OK},
		{"not synchronized", "no\n", nil, Warn},
		{"no timedatectl", "", errors.New("not found"), Warn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.clock, f.clockErr = c.out, c.err
			if r := find(t, Run(context.Background(), f.env), "Clock"); r.Level != c.want {
				t.Fatalf("got %+v", r)
			}
		})
	}
}

func TestEachSourceIsExamined(t *testing.T) {
	f := newFixture(t)
	cfg, err := config.Load(f.env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.dir, "etc", "secrets", "shop_mysql")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(dir, "db_password")
	if err := os.WriteFile(password, []byte("test-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Sources = append(cfg.Sources, config.Source{
		Name: "shop_mysql", Engine: config.EngineMySQL, Host: "10.0.0.11", Port: 3306, Username: "backup_ro",
		PasswordFile: password, SSLMode: "preferred", Databases: []string{"shop"},
	})
	if err := config.Save(f.env.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	f.engines = map[string]*fakeEngine{"shop_mysql": {
		client:   engine.ClientInfo{Text: "mysqldump Ver 8.0.36"},
		probeErr: &engine.ConnectError{Message: "cannot connect to MySQL at 10.0.0.11:3306"},
	}}
	results := Run(context.Background(), f.env)
	want := "Config=OK Permissions=OK Client tool main_pg=OK Database main_pg=OK Dump check main_pg/app_prod=OK Dump check main_pg/app_audit=OK " +
		"Client tool shop_mysql=OK Database shop_mysql=FAIL SFTP=OK Schedule=OK Clock=OK"
	if got := levels(results); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := find(t, Run(context.Background(), f.env), "Permissions")
	if r.Level != Fail || r.Fix != "Run: sudo chmod 0700 "+dir {
		t.Fatalf("got %+v", r)
	}
}
