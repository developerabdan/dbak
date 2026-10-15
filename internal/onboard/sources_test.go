package onboard

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/developerabdan/dbak/internal/config"
)

func withSourceRetention(tail []answer, extra ...answer) []answer {
	i := slices.IndexFunc(tail, func(a answer) bool { return a.match == "Monthly backups" })
	out := slices.Clone(tail[:i+1])
	out = append(out, extra...)
	return append(out, tail[i+1:]...)
}

func (h *harness) installOnePostgresSource(t *testing.T) {
	t.Helper()
	answers := h.onePostgresSource()
	answers = append(answers, h.destinationAnswers(t)...)
	answers = append(answers, tailAnswers()...)
	answers = append(answers, answer{match: "Run the first backup now?", value: false})
	if err := h.run(t, answers...); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
}

func loadSaved(t *testing.T, h *harness) *config.Config {
	t.Helper()
	cfg, err := config.Load(h.env.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestOnboardingWithTwoSources(t *testing.T) {
	h := newHarness(t)
	answers := append(sourceStart(config.EnginePostgres), answer{match: "database user for backup", value: "yes"})
	answers = append(answers, credentialAnswers("pg-secret")...)
	answers = append(answers,
		answer{match: "Select the databases"},
		answer{match: "Add another database source?", value: true},
	)
	answers = append(answers, sourceStart(config.EngineMySQL)...)
	answers = append(answers, answer{match: "database user for backup", value: "yes"})
	answers = append(answers, credentialAnswers("my-secret")...)
	answers = append(answers, answer{match: "Select the databases", value: []string{"app_prod"}}, noMoreSources())
	answers = append(answers, h.destinationAnswers(t)...)
	answers = append(answers, withSourceRetention(tailAnswers(),
		answer{match: "different retention for one or more", value: true},
		answer{match: "Retention for the source postgres", value: "global"},
		answer{match: "Retention for the source mysql", value: "custom"},
		answer{match: "Daily backups", value: "14"},
		answer{match: "Weekly backups", value: "0"},
		answer{match: "Monthly backups", value: "0"},
	)...)
	answers = append(answers, answer{match: "Run the first backup now?", value: false})
	if err := h.run(t, answers...); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	cfg := loadSaved(t, h)
	if len(cfg.Sources) != 2 || cfg.Sources[0].Name != "postgres" || cfg.Sources[1].Name != "mysql" {
		t.Fatalf("got sources %+v", cfg.Sources)
	}
	if cfg.Sources[1].Engine != config.EngineMySQL || cfg.Sources[1].Port != 3306 {
		t.Fatalf("got the second source %+v", cfg.Sources[1])
	}
	if cfg.Sources[0].Retention != nil || cfg.Sources[1].Retention == nil || *cfg.Sources[1].Retention != (config.Retention{Daily: 14}) {
		t.Fatalf("got retention %+v and %+v", cfg.Sources[0].Retention, cfg.Sources[1].Retention)
	}
	assertSecret(t, h.env.Paths.SourcePasswordFile("postgres"), "pg-secret")
	assertSecret(t, h.env.Paths.SourcePasswordFile("mysql"), "my-secret")
	out := h.ui.out.String()
	for _, want := range []string{"Source:            mysql", "  Retention:       14 daily, 0 weekly, 0 monthly"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not contain %q:\n%s", want, out)
		}
	}
}

func TestEditAddsAndRemovesASource(t *testing.T) {
	h := newHarness(t)
	h.installOnePostgresSource(t)

	answers := []answer{
		{match: "A config exists", value: "edit"},
		{match: "section to change", value: sectionSource},
		{match: "Select the database source to change.", value: actionAdd},
	}
	answers = append(answers, sourceStart(config.EngineMySQL)...)
	answers = append(answers, answer{match: "database user for backup", value: "yes"})
	answers = append(answers, credentialAnswers("my-secret")...)
	answers = append(answers,
		answer{match: "Select the databases"},
		answer{match: "Save this config?", value: true},
		answer{match: "Run a backup now?", value: false},
	)
	if err := h.run(t, answers...); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	cfg := loadSaved(t, h)
	if len(cfg.Sources) != 2 || cfg.Sources[1].Name != "mysql" {
		t.Fatalf("got sources %+v", cfg.Sources)
	}
	assertSecret(t, h.env.Paths.SourcePasswordFile("postgres"), "s3cret")
	assertSecret(t, h.env.Paths.SourcePasswordFile("mysql"), "my-secret")

	err := h.run(t,
		answer{match: "A config exists", value: "edit"},
		answer{match: "section to change", value: sectionSource},
		answer{match: "Select the database source to change.", value: actionRemove},
		answer{match: "Select the database source to remove.", value: "1"},
		answer{match: "Remove the source mysql?", value: true},
		answer{match: "Save this config?", value: true},
		answer{match: "Run a backup now?", value: false},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	cfg = loadSaved(t, h)
	if len(cfg.Sources) != 1 || cfg.Sources[0].Name != "postgres" {
		t.Fatalf("got sources %+v", cfg.Sources)
	}
	if _, err := os.Stat(filepath.Dir(h.env.Paths.SourcePasswordFile("mysql"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the password directory of the removed source still exists")
	}
	assertSecret(t, h.env.Paths.SourcePasswordFile("postgres"), "s3cret")
}

func TestEditRenamesASource(t *testing.T) {
	h := newHarness(t)
	h.installOnePostgresSource(t)
	answers := []answer{
		{match: "A config exists", value: "edit"},
		{match: "section to change", value: sectionSource},
		{match: "Select the database source to change.", value: "0"},
		{match: "database engine", value: config.EnginePostgres},
		{match: "Source name", value: "main_pg"},
		{match: "database user for backup", value: "yes"},
	}
	answers = append(answers, credentialAnswers("")...)
	answers = append(answers,
		answer{match: "Select the databases"},
		answer{match: "Save this config?", value: true},
		answer{match: "Run a backup now?", value: false},
	)
	if err := h.run(t, answers...); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	cfg := loadSaved(t, h)
	if cfg.Sources[0].Name != "main_pg" || cfg.Sources[0].PasswordFile != h.env.Paths.SourcePasswordFile("main_pg") {
		t.Fatalf("got source %+v", cfg.Sources[0])
	}
	assertSecret(t, cfg.Sources[0].PasswordFile, "s3cret")
	if _, err := os.Stat(h.env.Paths.SourcePasswordFile("postgres")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the old password file still exists")
	}
	if !strings.Contains(h.ui.out.String(), "WARNING: The backups of postgres on the SFTP server stay in the old directory.") {
		t.Fatalf("the output does not show the warning:\n%s", h.ui.out.String())
	}
}

func TestEditUpgradesAVersion1Config(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.env.Paths.SecretsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	password := h.env.Paths.Secret(passwordFileName)
	if err := os.WriteFile(password, []byte("old-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := strings.Join([]string{
		"version: 1",
		"source:",
		"  engine: postgres",
		"  host: 127.0.0.1",
		"  username: backup_ro",
		"  password_file: " + password,
		"  databases: [app_prod]",
		"destination:",
		"  type: sftp",
		"  host: backup.example.com",
		"  username: dbak",
		"  private_key_file: /etc/dbak/secrets/id_ed25519",
		"  known_hosts_file: /etc/dbak/secrets/known_hosts",
		"  remote_path: /backups",
		"schedule:",
		"  frequency: daily",
		"  time: \"02:00\"",
		"  timezone: UTC",
		"retention:",
		"  daily: 7",
		"",
	}, "\n")
	if err := os.WriteFile(h.env.Paths.ConfigPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	err := h.run(t,
		answer{match: "A config exists", value: "edit"},
		answer{match: "section to change", value: sectionRetention},
		answer{match: "Daily backups", value: "10"},
		answer{match: "Weekly backups"},
		answer{match: "Monthly backups"},
		answer{match: "Save this config?", value: true},
		answer{match: "Run a backup now?", value: false},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	data, err := os.ReadFile(h.env.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "version: 2") || !strings.Contains(string(data), "name: default") {
		t.Fatalf("the saved config is not version 2:\n%s", data)
	}
	cfg := loadSaved(t, h)
	if cfg.Sources[0].PasswordFile != password || cfg.Retention.Daily != 10 {
		t.Fatalf("got %+v %+v", cfg.Sources[0], cfg.Retention)
	}
	assertSecret(t, password, "old-secret")
}

func TestValidSourceName(t *testing.T) {
	h := newHarness(t)
	s := newSession(h.env)
	s.cfg.Sources = []config.Source{{Name: "main_pg"}, {Name: "shop"}}
	s.current = 1
	cases := []struct {
		name  string
		value string
		ok    bool
	}{
		{"new name", "shop_mysql", true},
		{"own name", "shop", true},
		{"name of a different source", "main_pg", false},
		{"bad characters", "shop-mysql", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := s.validSourceName(c.value); (err == nil) != c.ok {
				t.Fatalf("got %v for %q", err, c.value)
			}
		})
	}
}

func TestUniqueName(t *testing.T) {
	h := newHarness(t)
	s := newSession(h.env)
	s.cfg.Sources = []config.Source{{Name: "postgres"}, {Name: "postgres_2"}, {}}
	if got := s.uniqueName(config.EnginePostgres); got != "postgres_3" {
		t.Fatalf("got %s, want postgres_3", got)
	}
	if got := s.uniqueName(config.EngineMySQL); got != "mysql" {
		t.Fatalf("got %s, want mysql", got)
	}
}
