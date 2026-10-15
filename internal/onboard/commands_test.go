package onboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/developerabdan/dbak/internal/config"
)

func (h *harness) runCommand(t *testing.T, run func(ctx context.Context, env *Env) error, answers ...answer) error {
	t.Helper()
	h.ui = newFakeUI(t, answers...)
	h.env.UI = h.ui
	err := run(context.Background(), h.env)
	if err == nil {
		h.ui.done()
	}
	return err
}

func TestCommandsNeedAConfig(t *testing.T) {
	h := newHarness(t)
	err := h.runCommand(t, AddSource)
	if err == nil || !strings.Contains(err.Error(), "Run: sudo dbak init") {
		t.Fatalf("got %v", err)
	}
}

func TestAddSourceCommand(t *testing.T) {
	h := newHarness(t)
	h.installOnePostgresSource(t)
	schedules := len(h.schedules)
	answers := append(sourceStart(""), answer{match: "database user for backup", value: "yes"})
	answers[0].value = config.EngineMySQL
	answers = append(answers, credentialAnswers("my-secret")...)
	answers = append(answers,
		answer{match: "Select the databases"},
		answer{match: "Save this config?", value: true},
		answer{match: "Make a backup of the source mysql now?", value: true},
	)
	if err := h.runCommand(t, AddSource, answers...); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	cfg := loadSaved(t, h)
	if len(cfg.Sources) != 2 || cfg.Sources[1].Name != "mysql" {
		t.Fatalf("got sources %+v", cfg.Sources)
	}
	assertSecret(t, h.env.Paths.SourcePasswordFile("mysql"), "my-secret")
	assertSecret(t, h.env.Paths.SourcePasswordFile("postgres"), "s3cret")
	if !slices.Equal(h.backupSources[len(h.backupSources)-1:], []string{"mysql"}) {
		t.Fatalf("got backups of %v, want only the new source", h.backupSources)
	}
	if len(h.schedules) != schedules {
		t.Fatal("dbak source add installed the schedule again")
	}
}

func TestEditSourceCommand(t *testing.T) {
	h := newHarness(t)
	h.installOnePostgresSource(t)
	answers := []answer{
		{match: "database engine", value: config.EnginePostgres},
		{match: "Source name", value: "main_pg"},
		{match: "database user for backup", value: "yes"},
	}
	answers = append(answers, credentialAnswers("")...)
	answers = append(answers,
		answer{match: "Select the databases", value: []string{"app_prod"}},
		answer{match: "Save this config?", value: true},
		answer{match: "Make a backup of the source main_pg now?", value: false},
	)
	err := h.runCommand(t, func(ctx context.Context, env *Env) error { return EditSource(ctx, env, "postgres") }, answers...)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	cfg := loadSaved(t, h)
	if cfg.Sources[0].Name != "main_pg" || !slices.Equal(cfg.Sources[0].Databases, []string{"app_prod"}) {
		t.Fatalf("got source %+v", cfg.Sources[0])
	}
	assertSecret(t, h.env.Paths.SourcePasswordFile("main_pg"), "s3cret")

	err = h.runCommand(t, func(ctx context.Context, env *Env) error { return EditSource(ctx, env, "other") })
	if err == nil || !strings.Contains(err.Error(), "Use one of these sources: main_pg") {
		t.Fatalf("got %v", err)
	}
}

func TestRemoveSourceCommand(t *testing.T) {
	h := newHarness(t)
	h.installOnePostgresSource(t)
	remove := func(name string) func(context.Context, *Env) error {
		return func(_ context.Context, env *Env) error { return RemoveSource(env, name) }
	}
	err := h.runCommand(t, remove("postgres"))
	if err == nil || !strings.Contains(err.Error(), "the only source") {
		t.Fatalf("got %v", err)
	}
	cfg := loadSaved(t, h)
	cfg.Sources = append(cfg.Sources, config.Source{
		Name: "shop", Engine: config.EngineMySQL, Host: "127.0.0.1", Port: 3306, Username: "backup_ro",
		PasswordFile: h.env.Paths.SourcePasswordFile("shop"), SSLMode: "preferred", Databases: []string{"shop"},
	})
	if err := os.MkdirAll(filepath.Dir(h.env.Paths.SourcePasswordFile("shop")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.env.Paths.SourcePasswordFile("shop"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(h.env.Paths.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := h.runCommand(t, remove("shop"), answer{match: "Remove the source shop?", value: false}); !errors.Is(err, ErrAborted) {
		t.Fatalf("got %v, want ErrAborted", err)
	}
	if err := h.runCommand(t, remove("shop"), answer{match: "Remove the source shop?", value: true}); err != nil {
		t.Fatal(err)
	}
	if cfg := loadSaved(t, h); len(cfg.Sources) != 1 {
		t.Fatalf("got sources %+v", cfg.Sources)
	}
	if _, err := os.Stat(h.env.Paths.SourcePasswordFile("shop")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the password file of the removed source still exists")
	}
}

func TestConfigureNotificationsCommand(t *testing.T) {
	h := newHarness(t)
	h.installOnePostgresSource(t)
	err := h.runCommand(t, ConfigureNotifications,
		answer{match: "When must dbak send", value: config.NotifyAlways},
		answer{match: "Webhook URL", value: ""},
		answer{match: "Telegram?", value: true},
		answer{match: "Telegram bot token", value: "456:NEW"},
		answer{match: "Telegram chat ID", value: "-1003749220292"},
		answer{match: "Telegram topic ID", value: "316"},
		answer{match: "Healthcheck URL", value: "https://hc-ping.com/abc"},
		answer{match: "Save this config?", value: true},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	cfg := loadSaved(t, h)
	if cfg.Notify.When != config.NotifyAlways || cfg.Notify.TelegramChatID != "-1003749220292" || cfg.Notify.TelegramThreadID != 316 || cfg.Notify.WebhookURL != "" || cfg.Notify.HealthcheckURL != "https://hc-ping.com/abc" {
		t.Fatalf("got %+v", cfg.Notify)
	}
	assertSecret(t, cfg.Notify.TelegramTokenFile, "456:NEW")
	assertSecret(t, cfg.Sources[0].PasswordFile, "s3cret")
	if h.telegramSent[len(h.telegramSent)-1] != "456:NEW -1003749220292 topic 316" {
		t.Fatalf("got Telegram tests %v", h.telegramSent)
	}
	if !strings.Contains(h.ui.out.String(), "chat -1003749220292, topic 316, token ********") {
		t.Fatalf("the summary does not show the topic:\n%s", h.ui.out.String())
	}
	if strings.Contains(h.ui.out.String(), "456:NEW") {
		t.Fatal("the output contains the token")
	}
}
