package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/developerabdan/dbak/internal/encrypt"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func loadValid(t *testing.T) []byte {
	return loadFixture(t, "valid.yaml")
}

func TestParseValidConfig(t *testing.T) {
	cfg, err := Parse(loadValid(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(cfg.Sources))
	}
	pg, mysql := cfg.Sources[0], cfg.Sources[1]
	if pg.Name != "main_pg" || pg.Engine != EnginePostgres || len(pg.Databases) != 2 || pg.BinDir != "/usr/lib/postgresql/16/bin" {
		t.Fatalf("unexpected first source: %+v", pg)
	}
	if mysql.Name != "shop_mysql" || mysql.Engine != EngineMySQL {
		t.Fatalf("unexpected second source: %+v", mysql)
	}
	if cfg.Schedule.RandomizedDelay != "5m" || cfg.Retention.Monthly != 6 {
		t.Fatalf("unexpected values: %+v %+v", cfg.Schedule, cfg.Retention)
	}
}

func TestRetentionFor(t *testing.T) {
	cfg, err := Parse(loadValid(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RetentionFor(cfg.Sources[0]); got != cfg.Retention {
		t.Fatalf("got %+v, want the global retention %+v", got, cfg.Retention)
	}
	want := Retention{Daily: 14}
	if got := cfg.RetentionFor(cfg.Sources[1]); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSourceByName(t *testing.T) {
	cfg, err := Parse(loadValid(t))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := cfg.SourceByName("shop_mysql"); !ok || s.Engine != EngineMySQL {
		t.Fatalf("got %+v %v, want the shop_mysql source", s, ok)
	}
	if _, ok := cfg.SourceByName("none"); ok {
		t.Fatal("got a source for an unknown name")
	}
}

func TestParseConvertsVersion1(t *testing.T) {
	cfg, err := Parse(loadFixture(t, "legacy.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("got version %d, want %d", cfg.Version, CurrentVersion)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(cfg.Sources))
	}
	s := cfg.Sources[0]
	if s.Name != LegacySourceName || s.Engine != EnginePostgres || s.PasswordFile != "/etc/dbak/secrets/db_password" || len(s.Databases) != 2 {
		t.Fatalf("unexpected converted source: %+v", s)
	}
}

func TestParseRejectsMixedFormats(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"version 1 with sources", strings.Replace(string(loadValid(t)), "version: 2", "version: 1", 1)},
		{"version 2 with source", strings.Replace(string(loadFixture(t, "legacy.yaml")), "version: 1", "version: 2", 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse([]byte(c.data)); err == nil {
				t.Fatal("got no error, want an error")
			}
		})
	}
}

func TestParseRejectsBadValues(t *testing.T) {
	cases := []struct {
		name  string
		old   string
		new   string
		field string
	}{
		{"unknown version", "version: 2", "version: 3", "version"},
		{"bad source name", "name: main_pg", "name: main-pg", "sources[0].name"},
		{"empty source name", "name: main_pg", "name: \"\"", "sources[0].name"},
		{"duplicate source name", "name: shop_mysql", "name: main_pg", "sources[1].name"},
		{"unknown engine", "engine: postgres", "engine: oracle", "sources[0].engine"},
		{"bad postgres ssl mode", "ssl_mode: prefer", "ssl_mode: preferred", "sources[0].ssl_mode"},
		{"bad mysql ssl mode", "ssl_mode: preferred", "ssl_mode: prefer\n", "sources[1].ssl_mode"},
		{"empty host", "host: 10.0.0.10", "host: \"\"", "sources[0].host"},
		{"port out of range", "port: 5432", "port: 70000", "sources[0].port"},
		{"relative password file", "password_file: /etc/dbak/secrets/main_pg/db_password", "password_file: db_password", "sources[0].password_file"},
		{"relative bin dir", "bin_dir: /usr/lib/postgresql/16/bin", "bin_dir: bin", "sources[0].bin_dir"},
		{"bad database name", "- app_audit", "- app-audit", "sources[0].databases[1]"},
		{"duplicate database", "- app_audit", "- app_prod", "sources[0].databases[1]"},
		{"negative source retention", "daily: 14", "daily: -1", "sources[1].retention.daily"},
		{"zero source retention", "daily: 14", "daily: 0", "sources[1].retention"},
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
		{"negative telegram topic", "healthcheck_url: \"\"", "healthcheck_url: \"\"\n  telegram_thread_id: -1", "notify.telegram_thread_id"},
		{"telegram topic without chat", "healthcheck_url: \"\"", "healthcheck_url: \"\"\n  telegram_thread_id: 316", "notify.telegram_thread_id"},
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

func TestParseRejectsEmptySources(t *testing.T) {
	data := string(loadValid(t))
	start := strings.Index(data, "sources:")
	end := strings.Index(data, "destination:")
	data = data[:start] + "sources: []\n" + data[end:]
	_, err := Parse([]byte(data))
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Problems[0].Field != "sources" {
		t.Fatalf("got %v, want a problem for the field sources", err)
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
	data := strings.Replace(string(loadValid(t)), "engine: postgres", "engine: postgres\n    password: secret", 1)
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
	data = strings.Replace(data, "    port: 5432\n", "", 1)
	data = strings.Replace(data, "    ssl_mode: prefer\n", "", 1)
	data = strings.Replace(data, "  when: failure\n", "", 1)
	cfg, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := cfg.Sources[0]
	if s.Port != 3306 || s.SSLMode != "preferred" || cfg.Notify.When != NotifyFailure {
		t.Fatalf("defaults not applied: %+v %+v", s, cfg.Notify)
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
	if len(loaded.Sources) != 2 || loaded.Sources[1].Retention == nil || loaded.Sources[1].Retention.Daily != 14 {
		t.Fatalf("got %+v, want two sources with the retention of shop_mysql", loaded.Sources)
	}
}

func TestSaveWritesVersion2ForAConvertedConfig(t *testing.T) {
	cfg, err := Parse(loadFixture(t, "legacy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "version: 2") || !strings.Contains(text, "sources:") || strings.Contains(text, "\nsource:") {
		t.Fatalf("the saved config is not version 2:\n%s", text)
	}
	if strings.Contains(text, "bin_dir") || strings.Contains(text, "    retention:") {
		t.Fatalf("the saved config has empty optional fields:\n%s", text)
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

func TestEncryption(t *testing.T) {
	first, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	second, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	block := func(keys ...string) string {
		text := "encryption:\n  recipients:\n"
		for _, k := range keys {
			text += "    - " + k + "\n"
		}
		return text
	}
	cases := []struct {
		name    string
		extra   string
		enabled bool
		field   string
	}{
		{"no block", "", false, ""},
		{"empty list", "encryption:\n  recipients: []\n", false, ""},
		{"two keys", block(first.Public, second.Public), true, ""},
		{"bad key", block(first.Public, "age1notakey"), false, "encryption.recipients[1]"},
		{"private key", block(first.Private), false, "encryption.recipients[0]"},
		{"duplicate key", block(first.Public, first.Public), false, "encryption.recipients[1]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := Parse(append(loadValid(t), c.extra...))
			if c.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Encryption.Enabled() != c.enabled {
					t.Fatalf("got enabled %v, want %v", cfg.Encryption.Enabled(), c.enabled)
				}
				return
			}
			var verr *ValidationError
			if !errors.As(err, &verr) || verr.Problems[0].Field != c.field {
				t.Fatalf("got %v, want a problem for %s", err, c.field)
			}
			if strings.Contains(err.Error(), "AGE-SECRET-KEY") {
				t.Fatal("the error contains the private key")
			}
		})
	}
}

func TestSaveOmitsEmptyEncryption(t *testing.T) {
	cfg, err := Parse(loadValid(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "encryption") {
		t.Fatalf("the saved config has an empty encryption block:\n%s", data)
	}
}
