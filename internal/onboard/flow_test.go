package onboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/schedule"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/sftp/sftptest"
	"github.com/developerabdan/dbak/internal/state"
	"github.com/developerabdan/dbak/internal/system"
)

type harness struct {
	env          *Env
	ui           *fakeUI
	eng          *fakeEngine
	server       *sftptest.Server
	remoteRoot   string
	backups      int
	schedules    []config.Schedule
	scheduleErr  error
	telegramErrs []error
	telegramSent []string
	commands     [][]string
	sources      []config.Source
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{
		server:     sftptest.Start(t),
		remoteRoot: filepath.Join(dir, "remote"),
		eng: &fakeEngine{
			server:       engine.ServerInfo{Flavor: engine.FlavorPostgres, Version: "16.4", Major: 16},
			client:       engine.ClientInfo{Text: "pg_dump (PostgreSQL) 16.4", Major: 16},
			databases:    []string{"app_audit", "app_prod", "postgres"},
			privilegeBad: map[string]int{},
			schemaBad:    map[string]int{},
		},
	}
	if err := os.Mkdir(h.remoteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	h.env = &Env{
		Paths:      Paths{ConfigPath: filepath.Join(dir, "etc", "config.yaml"), StateDir: filepath.Join(dir, "state")},
		Owner:      system.CurrentOwner(),
		Hostname:   "vm-app-01",
		Timezone:   "UTC",
		PrimaryIP:  "10.0.0.5",
		OSFamily:   system.FamilyDebian,
		HasSystemd: true,
		Now:        func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) },
		NewEngine: func(source config.Source, _ string) (engine.Full, error) {
			h.sources = append(h.sources, source)
			return h.eng, nil
		},
		LookPath: func(string) (string, error) { return "/usr/bin/tool", nil },
		RunCommand: func(_ context.Context, args []string) error {
			h.commands = append(h.commands, args)
			return nil
		},
		DialSFTP: func(cfg sftp.Config) (SFTPClient, error) {
			cfg.Timeout = 5 * time.Second
			return sftp.Dial(cfg)
		},
		FetchHostKey: func(host string, port int) (ssh.PublicKey, error) {
			return sftp.FetchHostKey(host, port, 5*time.Second)
		},
		RunBackup: func(context.Context) (state.Run, error) {
			h.backups++
			return state.Run{Status: state.StatusSuccess, Databases: []state.Database{
				{Source: "postgres", Name: "app_prod", Status: state.StatusSuccess, SizeBytes: 2048, RemotePath: "/backups/vm-app-01/postgres/app_prod/app_prod_20261015T120000Z.dump"},
			}}, nil
		},
		SendTelegramTest: func(_ context.Context, token, chatID string) error {
			h.telegramSent = append(h.telegramSent, token+" "+chatID)
			if len(h.telegramErrs) > 0 {
				err := h.telegramErrs[0]
				h.telegramErrs = h.telegramErrs[1:]
				return err
			}
			return nil
		},
		InstallSchedule: func(_ context.Context, sc config.Schedule) (schedule.Result, error) {
			h.schedules = append(h.schedules, sc)
			if h.scheduleErr != nil {
				return schedule.Result{Warnings: []string{"The test warning."}}, h.scheduleErr
			}
			plan, err := schedule.NewPlan(sc)
			return schedule.Result{Mode: schedule.ModeSystemd, Plan: plan}, err
		},
	}
	return h
}

func (h *harness) run(t *testing.T, answers ...answer) error {
	t.Helper()
	h.ui = newFakeUI(t, answers...)
	h.env.UI = h.ui
	err := Run(context.Background(), h.env)
	if err == nil {
		h.ui.done()
	}
	return err
}

func (h *harness) allowGeneratedKey(t *testing.T) func() {
	return func() {
		data, err := os.ReadFile(h.env.Paths.Secret(privateKeyFileName) + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			t.Fatal(err)
		}
		h.server.Allow(key)
	}
}

func sourceStart(engineName string) []answer {
	return []answer{
		{match: "database engine", value: engineName},
		{match: "Source name"},
	}
}

func noMoreSources() answer {
	return answer{match: "Add another database source?", value: false}
}

func credentialAnswers(password string) []answer {
	return []answer{
		{match: "Database host", value: "127.0.0.1"},
		{match: "Database port"},
		{match: "Database user name"},
		{match: "Database password", value: password},
		{match: "SSL mode"},
	}
}

func (h *harness) onePostgresSource() []answer {
	answers := append(sourceStart(config.EnginePostgres), answer{match: "database user for backup", value: "yes"})
	answers = append(answers, credentialAnswers("s3cret")...)
	return append(answers, answer{match: "Select the databases"}, noMoreSources())
}

func (h *harness) destinationAnswers(t *testing.T) []answer {
	return []answer{
		{match: "SFTP host", value: h.server.Host},
		{match: "SFTP port", value: strconv.Itoa(h.server.Port)},
		{match: "SFTP user name", value: "dbak"},
		{match: "Remote path", value: h.remoteRoot},
		{match: "SFTP authentication", value: keyGenerate},
		{match: "Add the public key", hook: h.allowGeneratedKey(t)},
		{match: "correct host key", value: true},
	}
}

func tailAnswers() []answer {
	return []answer{
		{match: "How often", value: config.FrequencyDaily},
		{match: "Time of the backup", value: "02:00"},
		{match: "Timezone", value: "Asia/Jakarta"},
		{match: "Random delay", value: "5m"},
		{match: "Daily backups"},
		{match: "Weekly backups"},
		{match: "Monthly backups"},
		{match: "When must dbak send", value: config.NotifyFailure},
		{match: "Webhook URL", value: "https://hooks.example.com/secret-token"},
		{match: "Telegram?", value: true},
		{match: "Telegram bot token", value: "123:ABC"},
		{match: "Telegram chat ID", value: "-1001"},
		{match: "Healthcheck URL", value: ""},
		{match: "Save this config?", value: true},
	}
}

func TestFullOnboarding(t *testing.T) {
	h := newHarness(t)
	h.eng.probeErrors = []error{&engine.ConnectError{Message: "PostgreSQL rejected the user name or password. Make sure that the credentials are correct"}}
	h.eng.privilegeBad["app_audit"] = 1
	answers := append(sourceStart(config.EnginePostgres), answer{match: "database user for backup", value: "yes"})
	answers = append(answers, credentialAnswers("wrong")...)
	answers = append(answers, credentialAnswers("s3cret")...)
	answers = append(answers,
		answer{match: "Select the databases"},
		answer{match: "found problems", value: "retry"},
		noMoreSources(),
	)
	answers = append(answers, h.destinationAnswers(t)...)
	answers = append(answers, tailAnswers()...)
	answers = append(answers, answer{match: "Run the first backup now?", value: true})
	if err := h.run(t, answers...); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	out := h.ui.out.String()
	for _, want := range []string{
		"ERROR: PostgreSQL rejected the user name or password.",
		"Connected to PostgreSQL 16.4.",
		`"public"."audit_log"`,
		"GRANT SELECT ON ALL TABLES",
		"OK: The dump check of app_audit passed.",
		"ssh-ed25519 ",
		"authorized_keys",
		"OK: The test upload to " + filepath.Join(h.remoteRoot, "vm-app-01") + " passed.",
		"02:00 Asia/Jakarta = 19:00 UTC on the day before",
		"Source:            postgres",
		"  Password:        ********",
		"Webhook:           https://hooks.example.com/********",
		"chat -1001, token ********",
		"OK: dbak installed the systemd timer dbak.timer: daily at 02:00 Asia/Jakarta.",
		"OK: postgres/app_prod: 2.0 KiB",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not contain %q", want)
		}
	}
	for _, secret := range []string{"s3cret", "123:ABC", "secret-token"} {
		if strings.Contains(out, secret) {
			t.Errorf("the output contains the secret %q", secret)
		}
	}
	cfg, err := config.Load(h.env.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].Name != "postgres" {
		t.Fatalf("got sources %+v, want one source with the name postgres", cfg.Sources)
	}
	if !slices.Equal(cfg.Sources[0].Databases, []string{"app_audit", "app_prod"}) {
		t.Fatalf("got databases %v, want postgres not selected", cfg.Sources[0].Databases)
	}
	if cfg.Schedule.Timezone != "Asia/Jakarta" || cfg.Schedule.RandomizedDelay != "5m" || cfg.Destination.RemotePath != h.remoteRoot {
		t.Fatalf("unexpected config: %+v %+v", cfg.Schedule, cfg.Destination)
	}
	if cfg.Sources[0].PasswordFile != h.env.Paths.SourcePasswordFile("postgres") {
		t.Fatalf("got password file %s", cfg.Sources[0].PasswordFile)
	}
	assertSecret(t, cfg.Sources[0].PasswordFile, "s3cret")
	assertSecret(t, cfg.Notify.TelegramTokenFile, "123:ABC")
	assertMode(t, h.env.Paths.SecretsDir(), 0o700)
	assertMode(t, filepath.Dir(cfg.Sources[0].PasswordFile), 0o700)
	assertMode(t, h.env.Paths.ConfigPath, 0o600)
	assertMode(t, cfg.Destination.PrivateKeyFile, 0o600)
	assertMode(t, cfg.Destination.KnownHostsFile, 0o600)
	if h.backups != 1 {
		t.Fatalf("got %d backups, want 1", h.backups)
	}
	if !slices.Equal(h.telegramSent, []string{"123:ABC -1001"}) {
		t.Fatalf("got Telegram tests %v", h.telegramSent)
	}
	if len(h.schedules) != 1 || h.schedules[0] != cfg.Schedule {
		t.Fatalf("got installed schedules %+v, want %+v", h.schedules, cfg.Schedule)
	}
	if h.sources[len(h.sources)-1].Port != 5432 || h.sources[len(h.sources)-1].SSLMode != "prefer" {
		t.Fatalf("unexpected source defaults: %+v", h.sources[len(h.sources)-1])
	}
	data, err := os.ReadFile(h.env.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret") || strings.Contains(string(data), "123:ABC") {
		t.Fatal("the config file contains a secret")
	}
}

func assertSecret(t *testing.T, file, want string) {
	t.Helper()
	got, err := config.ReadSecret(file)
	if err != nil || got != want {
		t.Fatalf("got %q %v from %s", got, err, file)
	}
	assertMode(t, file, 0o600)
}

func assertMode(t *testing.T, file string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s has mode %v, want %v", file, info.Mode().Perm(), want)
	}
}

func TestOnboardingWithUserHelper(t *testing.T) {
	h := newHarness(t)
	h.env.HasSystemd = false
	answers := append(sourceStart(config.EngineMySQL), []answer{
		{match: "database user for backup", value: "no"},
		{match: "version of the database server", value: "mysql-8.0.20"},
		{match: "User name for the backup user"},
		{match: "Database names", value: "app_prod"},
		{match: "connects from", value: "%"},
		{match: "use the host %?", value: false},
		{match: "connects from"},
		{match: "I ran the SQL", value: true},
		{match: "Database host", value: "10.0.0.10"},
		{match: "Database port"},
		{match: "Database user name"},
		{match: "Database password", value: ""},
		{match: "SSL mode"},
		{match: "Select the databases", value: []string{"app_prod"}},
		noMoreSources(),
	}...)
	h.eng.server = engine.ServerInfo{Flavor: engine.FlavorMySQL, Version: "8.0.36", Major: 8}
	h.eng.client = engine.ClientInfo{Text: "mysqldump  Ver 8.0.36", Major: 8}
	answers = append(answers, h.destinationAnswers(t)...)
	answers = append(answers, tailAnswers()...)
	answers = append(answers, answer{match: "Run the first backup now?", value: false})
	if err := h.run(t, answers...); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	out := h.ui.out.String()
	if !strings.Contains(out, "dbak will use cron") {
		t.Error("the output does not show the cron fallback")
	}
	if !strings.Contains(out, "CREATE USER 'backup_ro'@'10.0.0.5' IDENTIFIED BY '") {
		t.Errorf("the output does not show the SQL with the detected IP address:\n%s", out)
	}
	cfg, err := config.Load(h.env.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	src := cfg.Sources[0]
	if src.Name != "mysql" || src.Port != 3306 || src.SSLMode != "preferred" || src.Username != "backup_ro" {
		t.Fatalf("unexpected source: %+v", src)
	}
	password, err := config.ReadSecret(src.PasswordFile)
	if err != nil || len(password) != 32 || !strings.Contains(out, password) {
		t.Fatalf("got password %q %v, want the generated password", password, err)
	}
	if h.backups != 0 {
		t.Fatal("dbak made a backup after the answer No")
	}
}

func TestMissingClientToolDeclined(t *testing.T) {
	h := newHarness(t)
	h.env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	err := h.run(t, append(sourceStart(config.EnginePostgres),
		answer{match: "Install the package postgresql-client now?", value: false},
	)...)
	if err == nil || !strings.Contains(err.Error(), "pg_dump is not installed") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(h.ui.out.String(), "apt-get update\napt-get install -y postgresql-client") {
		t.Fatalf("the output does not show the commands:\n%s", h.ui.out.String())
	}
	if len(h.commands) != 0 {
		t.Fatal("dbak ran a command after the answer No")
	}
}

func TestMissingClientToolInstalled(t *testing.T) {
	h := newHarness(t)
	installed := false
	h.env.LookPath = func(string) (string, error) {
		if installed {
			return "/usr/bin/pg_dump", nil
		}
		return "", errors.New("not found")
	}
	h.env.RunCommand = func(_ context.Context, args []string) error {
		h.commands = append(h.commands, args)
		installed = true
		return nil
	}
	h.ui = newFakeUI(t, answer{match: "Install the package", value: true})
	h.env.UI = h.ui
	s := newSession(h.env)
	s.cfg.Sources = []config.Source{{Name: "postgres", Engine: config.EnginePostgres}}
	if err := s.ensureClientTool(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.commands) != 2 || h.commands[1][0] != "apt-get" {
		t.Fatalf("got commands %v", h.commands)
	}
}

func TestOldPostgresClientStops(t *testing.T) {
	h := newHarness(t)
	h.eng.client = engine.ClientInfo{Text: "pg_dump (PostgreSQL) 14.10", Major: 14}
	answers := append(sourceStart(config.EnginePostgres), answer{match: "database user for backup", value: "yes"})
	answers = append(answers, credentialAnswers("s3cret")...)
	err := h.run(t, answers...)
	if err == nil || !strings.Contains(err.Error(), "pg_dump 14 cannot make a backup of a PostgreSQL 16 server") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(h.ui.out.String(), "postgresql-client-16") {
		t.Fatalf("the output does not show the PGDG commands:\n%s", h.ui.out.String())
	}
}

func TestDeclinedHostKeyStops(t *testing.T) {
	h := newHarness(t)
	h.ui = newFakeUI(t,
		answer{match: "SFTP host", value: h.server.Host},
		answer{match: "SFTP port", value: strconv.Itoa(h.server.Port)},
		answer{match: "SFTP user name", value: "dbak"},
		answer{match: "Remote path", value: h.remoteRoot},
		answer{match: "SFTP authentication", value: keyGenerate},
		answer{match: "Add the public key"},
		answer{match: "correct host key", value: false},
	)
	h.env.UI = h.ui
	err := newSession(h.env).configureDestination(context.Background())
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("got %v, want ErrAborted", err)
	}
	if _, err := os.Stat(h.env.Paths.Secret(knownHostsFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dbak wrote known_hosts after the answer No")
	}
}

func TestRejectedKeyGivesChoice(t *testing.T) {
	h := newHarness(t)
	h.ui = newFakeUI(t,
		answer{match: "SFTP host", value: h.server.Host},
		answer{match: "SFTP port", value: strconv.Itoa(h.server.Port)},
		answer{match: "SFTP user name", value: "dbak"},
		answer{match: "Remote path", value: h.remoteRoot},
		answer{match: "SFTP authentication", value: keyGenerate},
		answer{match: "Add the public key"},
		answer{match: "correct host key", value: true},
		answer{match: "SFTP check failed", value: "stop"},
	)
	h.env.UI = h.ui
	err := newSession(h.env).configureDestination(context.Background())
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("got %v, want ErrAborted", err)
	}
	if !strings.Contains(h.ui.out.String(), "authorized_keys on the server") {
		t.Fatalf("the output does not explain the rejected key:\n%s", h.ui.out.String())
	}
}

func TestExistingConfigEditRetention(t *testing.T) {
	h := newHarness(t)
	answers := h.onePostgresSource()
	answers = append(answers, h.destinationAnswers(t)...)
	answers = append(answers, tailAnswers()...)
	answers = append(answers, answer{match: "Run the first backup now?", value: false})
	if err := h.run(t, answers...); err != nil {
		t.Fatal(err)
	}
	err := h.run(t,
		answer{match: "A config exists", value: "edit"},
		answer{match: "section to change", value: sectionRetention},
		answer{match: "Daily backups", value: "14"},
		answer{match: "Weekly backups", value: "0"},
		answer{match: "Monthly backups", value: "0"},
		answer{match: "Save this config?", value: true},
		answer{match: "Run a backup now?", value: false},
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(h.env.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retention != (config.Retention{Daily: 14}) {
		t.Fatalf("got %+v", cfg.Retention)
	}
	if len(h.schedules) != 2 {
		t.Fatalf("got %d schedule installs, want 2", len(h.schedules))
	}
	assertSecret(t, cfg.Sources[0].PasswordFile, "s3cret")
	assertSecret(t, cfg.Notify.TelegramTokenFile, "123:ABC")
}

func TestScheduleInstallFailureContinues(t *testing.T) {
	h := newHarness(t)
	h.scheduleErr = errors.New("systemd does not accept the schedule")
	answers := h.onePostgresSource()
	answers = append(answers, h.destinationAnswers(t)...)
	answers = append(answers, tailAnswers()...)
	answers = append(answers, answer{match: "Run the first backup now?", value: true})
	if err := h.run(t, answers...); err != nil {
		t.Fatal(err)
	}
	out := h.ui.out.String()
	for _, want := range []string{
		"WARNING: The test warning.",
		"ERROR: systemd does not accept the schedule.",
		"no schedule is installed. Correct the problem, then run: sudo dbak schedule",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not contain %q:\n%s", want, out)
		}
	}
	if h.backups != 1 {
		t.Fatalf("got %d backups, want 1", h.backups)
	}
}

func TestTelegramTestFailure(t *testing.T) {
	h := newHarness(t)
	h.telegramErrs = []error{errors.New("the server returned HTTP status 400: Bad Request: chat not found"), errors.New("the server returned HTTP status 401")}
	h.ui = newFakeUI(t,
		answer{match: "When must dbak send", value: config.NotifyFailure},
		answer{match: "Webhook URL", value: ""},
		answer{match: "Telegram?", value: true},
		answer{match: "Telegram bot token", value: "123:ABC"},
		answer{match: "Telegram chat ID", value: "-1"},
		answer{match: "What do you want to do?", value: "retry"},
		answer{match: "Telegram bot token", value: ""},
		answer{match: "Telegram chat ID", value: "-2"},
		answer{match: "What do you want to do?", value: "skip"},
		answer{match: "Healthcheck URL", value: ""},
	)
	h.env.UI = h.ui
	s := newSession(h.env)
	if err := s.configureNotify(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.ui.done()
	if !slices.Equal(h.telegramSent, []string{"123:ABC -1", "123:ABC -2"}) {
		t.Fatalf("got Telegram tests %v", h.telegramSent)
	}
	if s.cfg.Notify.TelegramChatID != "" || s.cfg.Notify.TelegramTokenFile != "" || s.telegramToken != "" {
		t.Fatalf("got Telegram settings after skip: %+v", s.cfg.Notify)
	}
	if !strings.Contains(h.ui.out.String(), "ERROR: The test message failed: The server returned HTTP status 400: Bad Request: chat not found.") {
		t.Fatalf("got output:\n%s", h.ui.out.String())
	}
}

func TestExistingConfigCancel(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Dir(h.env.Paths.ConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.env.Paths.ConfigPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := h.run(t, answer{match: "A config exists", value: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.ui.out.String(), "WARNING: The config is not valid") {
		t.Fatalf("the output does not show the invalid config:\n%s", h.ui.out.String())
	}
}

func TestRetentionRejectsZeroTotal(t *testing.T) {
	h := newHarness(t)
	h.ui = newFakeUI(t,
		answer{match: "Daily backups", value: "0"},
		answer{match: "Weekly backups", value: "0"},
		answer{match: "Monthly backups", value: "0"},
		answer{match: "Daily backups", value: "3"},
		answer{match: "Weekly backups", value: "0"},
		answer{match: "Monthly backups", value: "0"},
	)
	h.env.UI = h.ui
	s := newSession(h.env)
	if err := s.configureRetention(); err != nil {
		t.Fatal(err)
	}
	if s.cfg.Retention.Daily != 3 || !strings.Contains(h.ui.out.String(), "The total is zero") {
		t.Fatalf("got %+v", s.cfg.Retention)
	}
}

func TestMaskURL(t *testing.T) {
	cases := map[string]string{
		"":                                    "not set",
		"https://hc-ping.com":                 "https://hc-ping.com",
		"https://hc-ping.com/1234-uuid":       "https://hc-ping.com/********",
		"https://hooks.slack.com/services/T0": "https://hooks.slack.com/********",
	}
	for in, want := range cases {
		if got := maskURL(in); got != want {
			t.Errorf("maskURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTimezoneWarningForUTCServer(t *testing.T) {
	h := newHarness(t)
	h.ui = newFakeUI(t)
	h.env.UI = h.ui
	h.env.Timezone = "Etc/UTC"
	s := newSession(h.env)
	s.cfg.Schedule.Timezone = "UTC"
	s.timezoneWarning()
	if !strings.Contains(h.ui.out.String(), "This server uses UTC") {
		t.Fatalf("got %q", h.ui.out.String())
	}
}
