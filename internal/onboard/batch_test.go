package onboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/sftp/sftptest"
)

func (h *harness) batchInput(t *testing.T, edit func(*config.Config)) string {
	t.Helper()
	dir := t.TempDir()
	signer, private := sftptest.NewSigner(t)
	h.server.Allow(signer.PublicKey())
	keyFile := sftptest.WritePrivateKey(t, dir, private)
	hostKey, err := sftp.FetchHostKey(h.server.Host, h.server.Port, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(sftp.KnownHostsLine(h.server.Host, h.server.Port, hostKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version: config.CurrentVersion,
		Sources: []config.Source{{
			Name: "main_pg", Engine: config.EnginePostgres, Host: "10.0.0.10", Port: 5432, Username: "backup_ro",
			PasswordFile: filepath.Join(dir, "missing_password"), SSLMode: "prefer", Databases: []string{"app_prod"},
		}},
		Destination: config.Destination{
			Type: "sftp", Host: h.server.Host, Port: h.server.Port, Username: "dbak",
			PrivateKeyFile: keyFile, KnownHostsFile: knownHosts, RemotePath: h.remoteRoot,
		},
		Schedule:  config.Schedule{Frequency: config.FrequencyDaily, Time: "02:00", Timezone: "Asia/Jakarta", RandomizedDelay: "0"},
		Retention: config.Retention{Daily: 7, Weekly: 4, Monthly: 6},
		Notify:    config.Notify{When: config.NotifyFailure},
	}
	if edit != nil {
		edit(cfg)
	}
	input := filepath.Join(dir, "input.yaml")
	if err := config.Save(input, cfg); err != nil {
		t.Fatal(err)
	}
	return input
}

func (h *harness) runBatch(t *testing.T, input string, secrets Secrets) error {
	t.Helper()
	h.ui = newFakeUI(t)
	h.env.UI = h.ui
	return RunFromFile(context.Background(), h.env, input, secrets)
}

func TestBatchInit(t *testing.T) {
	h := newHarness(t)
	input := h.batchInput(t, func(c *config.Config) {
		c.Notify.TelegramChatID = "-1001"
	})
	err := h.runBatch(t, input, Secrets{DBPassword: "s3cret", TelegramToken: "123:ABC"})
	if err != nil {
		t.Fatalf("got %v\n%s", err, h.ui.out.String())
	}
	cfg, err := config.Load(h.env.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sources[0].PasswordFile != h.env.Paths.SourcePasswordFile("main_pg") ||
		cfg.Destination.PrivateKeyFile != h.env.Paths.Secret(privateKeyFileName) ||
		cfg.Destination.KnownHostsFile != h.env.Paths.Secret(knownHostsFileName) ||
		cfg.Notify.TelegramTokenFile != h.env.Paths.Secret(telegramFileName) {
		t.Fatalf("the config does not use the dbak secret paths: %+v %+v %+v", cfg.Sources, cfg.Destination, cfg.Notify)
	}
	assertSecret(t, cfg.Sources[0].PasswordFile, "s3cret")
	assertSecret(t, cfg.Notify.TelegramTokenFile, "123:ABC")
	assertMode(t, cfg.Destination.PrivateKeyFile, 0o600)
	assertMode(t, cfg.Destination.KnownHostsFile, 0o600)
	if len(h.schedules) != 1 || h.backups != 0 {
		t.Fatalf("got %d schedule installs and %d backups", len(h.schedules), h.backups)
	}
	out := h.ui.out.String()
	for _, want := range []string{"Connected to PostgreSQL 16.4.", "OK: The dump check of app_prod passed.", "The test upload to", "OK: dbak installed the systemd timer"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not contain %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"s3cret", "123:ABC"} {
		if strings.Contains(out, secret) {
			t.Errorf("the output contains the secret %q", secret)
		}
	}
}

func TestBatchInitUsesPasswordFile(t *testing.T) {
	h := newHarness(t)
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := h.batchInput(t, func(c *config.Config) { c.Sources[0].PasswordFile = passwordFile })
	if err := h.runBatch(t, input, Secrets{}); err != nil {
		t.Fatal(err)
	}
	assertSecret(t, h.env.Paths.SourcePasswordFile("main_pg"), "from-file")
}

func TestBatchInitFailures(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*config.Config)
		secrets Secrets
		setup   func(h *harness)
		code    int
		message string
	}{
		{"no password", nil, Secrets{}, nil, app.ExitConfigInvalid, "DBAK_DB_PASSWORD_MAIN_PG"},
		{"one password for two sources", addMySQLSource, Secrets{DBPassword: "x"}, nil, app.ExitConfigInvalid, "Set DBAK_DB_PASSWORD_MAIN_PG"},
		{"missing bin_dir tool", func(c *config.Config) { c.Sources[0].BinDir = "/opt/pg/bin" }, Secrets{DBPassword: "x"}, func(h *harness) {
			h.env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
		}, app.ExitConfigInvalid, "/opt/pg/bin/pg_dump does not exist"},
		{"no Telegram token", func(c *config.Config) { c.Notify.TelegramChatID = "-1" }, Secrets{DBPassword: "x"}, nil, app.ExitConfigInvalid, "DBAK_TELEGRAM_TOKEN"},
		{"no known_hosts", func(c *config.Config) { c.Destination.KnownHostsFile = "/missing/known_hosts" }, Secrets{DBPassword: "x"}, nil, app.ExitConfigInvalid, "ssh-keyscan"},
		{"invalid config", func(c *config.Config) { c.Retention = config.Retention{} }, Secrets{DBPassword: "x"}, nil, app.ExitConfigInvalid, "retention"},
		{"database not available", nil, Secrets{DBPassword: "x"}, func(h *harness) {
			h.eng.probeErrors = []error{&engine.ConnectError{Message: "cannot connect to PostgreSQL"}}
		}, app.ExitDatabase, "cannot connect"},
		{"unknown database", func(c *config.Config) { c.Sources[0].Databases = []string{"app_missing"} }, Secrets{DBPassword: "x"}, nil, app.ExitConfigInvalid, "app_missing does not exist"},
		{"dump check fails", nil, Secrets{DBPassword: "x"}, func(h *harness) { h.eng.privilegeBad["app_prod"] = 1 }, app.ExitGeneral, "dump check failed"},
		{"missing client tool", nil, Secrets{DBPassword: "x"}, func(h *harness) {
			h.env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
		}, app.ExitGeneral, "pg_dump is not installed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			if c.setup != nil {
				c.setup(h)
			}
			input := h.batchInput(t, c.edit)
			err := h.runBatch(t, input, c.secrets)
			if app.ExitCode(err) != c.code || err == nil || !strings.Contains(err.Error(), c.message) {
				t.Fatalf("got exit code %d and %v, want %d and %q", app.ExitCode(err), err, c.code, c.message)
			}
			if _, err := os.Stat(h.env.Paths.ConfigPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("dbak wrote the config after a failed check")
			}
		})
	}
}

func TestBatchInitSFTPFailure(t *testing.T) {
	h := newHarness(t)
	input := h.batchInput(t, nil)
	h.env.DialSFTP = func(sftp.Config) (SFTPClient, error) { return nil, sftp.ErrHostKeyChanged }
	err := h.runBatch(t, input, Secrets{DBPassword: "x"})
	if app.ExitCode(err) != app.ExitUploadFailed || !errors.Is(err, sftp.ErrHostKeyChanged) {
		t.Fatalf("got %d %v", app.ExitCode(err), err)
	}
}

func TestBatchInitWithoutPasswordFileField(t *testing.T) {
	h := newHarness(t)
	input := h.batchInput(t, func(c *config.Config) { c.Sources[0].PasswordFile = "" })
	if err := h.runBatch(t, input, Secrets{DBPassword: "from-env"}); err != nil {
		t.Fatal(err)
	}
	assertSecret(t, h.env.Paths.SourcePasswordFile("main_pg"), "from-env")
}

func addMySQLSource(c *config.Config) {
	c.Sources = append(c.Sources, config.Source{
		Name: "shop_mysql", Engine: config.EngineMySQL, Host: "10.0.0.11", Port: 3306, Username: "backup_ro",
		SSLMode: "preferred", Databases: []string{"app_prod"},
	})
}

func TestBatchInitWithTwoSources(t *testing.T) {
	h := newHarness(t)
	input := h.batchInput(t, addMySQLSource)
	secrets := Secrets{DBPasswords: map[string]string{"MAIN_PG": "pg-secret", "SHOP_MYSQL": "my-secret"}}
	if err := h.runBatch(t, input, secrets); err != nil {
		t.Fatalf("got %v\n%s", err, h.ui.out.String())
	}
	cfg, err := config.Load(h.env.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(cfg.Sources))
	}
	assertSecret(t, h.env.Paths.SourcePasswordFile("main_pg"), "pg-secret")
	assertSecret(t, h.env.Paths.SourcePasswordFile("shop_mysql"), "my-secret")
	if len(h.sources) != 2 || h.sources[1].Name != "shop_mysql" {
		t.Fatalf("dbak did not examine both sources: %+v", h.sources)
	}
	out := h.ui.out.String()
	for _, want := range []string{"## Database source main_pg", "## Database source shop_mysql", "Source:            shop_mysql"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not contain %q:\n%s", want, out)
		}
	}
}

func TestBatchInitConvertsVersion1(t *testing.T) {
	h := newHarness(t)
	input := h.batchInput(t, nil)
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.NewReplacer("version: 2", "version: 1", "sources:\n    - name: main_pg\n      ", "source:\n    ", "\n      ", "\n    ").Replace(string(data))
	if err := os.WriteFile(input, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.runBatch(t, input, Secrets{DBPassword: "from-env"}); err != nil {
		t.Fatalf("got %v\ninput:\n%s", err, legacy)
	}
	cfg, err := config.Load(h.env.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sources[0].Name != config.LegacySourceName {
		t.Fatalf("got source %+v, want the name %s", cfg.Sources[0], config.LegacySourceName)
	}
	assertSecret(t, h.env.Paths.SourcePasswordFile(config.LegacySourceName), "from-env")
}
