package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadValid(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "valid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseValidConfig(t *testing.T) {
	cfg, err := Parse(loadValid(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Source.Engine != EnginePostgres || len(cfg.Source.Databases) != 2 {
		t.Fatalf("unexpected source: %+v", cfg.Source)
	}
	if cfg.Schedule.RandomizedDelay != "5m" || cfg.Retention.Monthly != 6 {
		t.Fatalf("unexpected values: %+v %+v", cfg.Schedule, cfg.Retention)
	}
}

func TestParseRejectsBadValues(t *testing.T) {
	cases := []struct {
		name  string
		old   string
		new   string
		field string
	}{
		{"unknown version", "version: 1", "version: 2", "version"},
		{"unknown engine", "engine: postgres", "engine: oracle", "source.engine"},
		{"bad postgres ssl mode", "ssl_mode: prefer", "ssl_mode: preferred", "source.ssl_mode"},
		{"empty host", "host: 10.0.0.10", "host: \"\"", "source.host"},
		{"port out of range", "port: 5432", "port: 70000", "source.port"},
		{"relative password file", "password_file: /etc/dbak/secrets/db_password", "password_file: db_password", "source.password_file"},
		{"bad database name", "- app_audit", "- app-audit", "source.databases[1]"},
		{"duplicate database", "- app_audit", "- app_prod", "source.databases[1]"},
		{"unknown destination type", "type: sftp", "type: s3", "destination.type"},
		{"relative remote path", "remote_path: /backups", "remote_path: backups", "destination.remote_path"},
		{"bad clock time", "time: \"02:00\"", "time: \"2am\"", "schedule.time"},
		{"unknown frequency", "frequency: daily", "frequency: hourly", "schedule.frequency"},
		{"unknown timezone", "timezone: Asia/Jakarta", "timezone: Mars/Base", "schedule.timezone"},
		{"bad delay", "randomized_delay: 5m", "randomized_delay: soon", "schedule.randomized_delay"},
		{"negative retention", "daily: 7", "daily: -1", "retention.daily"},
		{"unknown notify mode", "when: failure", "when: sometimes", "notify.when"},
		{"bad webhook url", "webhook_url: \"\"", "webhook_url: \"ftp://x\"", "notify.webhook_url"},
		{"telegram chat without token", "telegram_chat_id: \"\"", "telegram_chat_id: \"123\"", "notify.telegram_chat_id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := strings.Replace(string(loadValid(t)), c.old, c.new, 1)
			_, err := Parse([]byte(data))
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("got %v, want a validation error", err)
			}
			for _, p := range verr.Problems {
				if p.Field == c.field {
					return
				}
			}
			t.Fatalf("no problem for field %s in %v", c.field, verr)
		})
	}
}

func TestParseRejectsZeroRetentionTotal(t *testing.T) {
	data := string(loadValid(t))
	data = strings.Replace(data, "daily: 7", "daily: 0", 1)
	data = strings.Replace(data, "weekly: 4", "weekly: 0", 1)
	data = strings.Replace(data, "monthly: 6", "monthly: 0", 1)
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "retention:") {
		t.Fatalf("got %v, want a retention error", err)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	data := strings.Replace(string(loadValid(t)), "engine: postgres", "engine: postgres\n  password: secret", 1)
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("got %v, want an unknown field error", err)
	}
}

func TestParseRejectsEmptyFile(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Fatal("got no error, want an error")
	}
}

func TestParseAppliesDefaults(t *testing.T) {
	data := string(loadValid(t))
	data = strings.Replace(data, "engine: postgres", "engine: mysql", 1)
	data = strings.Replace(data, "  port: 5432\n", "", 1)
	data = strings.Replace(data, "  ssl_mode: prefer\n", "", 1)
	data = strings.Replace(data, "  when: failure\n", "", 1)
	cfg, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Source.Port != 3306 || cfg.Source.SSLMode != "preferred" || cfg.Notify.When != NotifyFailure {
		t.Fatalf("defaults not applied: %+v %+v", cfg.Source, cfg.Notify)
	}
}

func TestEveryNHoursSchedule(t *testing.T) {
	data := strings.Replace(string(loadValid(t)), "frequency: daily", "frequency: every_n_hours", 1)
	if _, err := Parse([]byte(data)); err == nil {
		t.Fatal("got no error for every_hours 0, want an error")
	}
	data = strings.Replace(data, "every_hours: 0", "every_hours: 6", 1)
	if _, err := Parse([]byte(data)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveAndLoad(t *testing.T) {
	cfg, err := Parse(loadValid(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("got mode %v, want 0600", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Destination != cfg.Destination {
		t.Fatalf("got %+v, want %+v", loaded.Destination, cfg.Destination)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err == nil || !strings.Contains(err.Error(), "dbak init") {
		t.Fatalf("got %v, want a message about dbak init", err)
	}
}

func TestReadSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db_password")
	if err := os.WriteFile(path, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSecret(path)
	if err != nil || got != "s3cret" {
		t.Fatalf("got %q %v", got, err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(empty); err == nil {
		t.Fatal("got no error for an empty secret")
	}
}
