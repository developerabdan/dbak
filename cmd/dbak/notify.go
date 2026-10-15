package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/notify"
	"github.com/developerabdan/dbak/internal/onboard"
	"github.com/developerabdan/dbak/internal/system"
)

func newNotifyCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Change the webhook, Telegram, and healthcheck notifications with interactive questions.",
		Long:  "Change the webhook, Telegram, and healthcheck notifications with interactive questions. dbak sends a test message to Telegram before it saves the config. You must run this command as root.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOnboarding(cmd, opts, "notify", onboard.ConfigureNotifications)
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "test",
		Short: "Send a test message to the webhook and to Telegram.",
		Long:  "Send a test message to the webhook and to Telegram. The webhook gets a JSON body with the status test. dbak does not send a request to the healthcheck URL, because a request changes the state of the check.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNotifyTest(cmd.Context(), cmd, opts)
		},
	})
	return cmd
}

var errNoTargets = errors.New("no notification target is set. To set a webhook or Telegram, run: sudo dbak notify")

func runNotifyTest(ctx context.Context, cmd *cobra.Command, opts *globalOptions) error {
	cfg, err := loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	hostname, err := system.Hostname()
	if err != nil {
		return app.Exit(app.ExitGeneral, err)
	}
	token := ""
	if cfg.Notify.TelegramChatID != "" {
		if token, err = config.ReadSecret(cfg.Notify.TelegramTokenFile); err != nil {
			return app.Exit(app.ExitGeneral, fmt.Errorf("%w. Run the command with sudo", err))
		}
	}
	n := notify.New(cfg.Notify, token, slog.New(slog.DiscardHandler))
	results := n.Test(ctx, hostname, time.Now())
	if len(results) == 0 {
		return app.Exit(app.ExitGeneral, errNoTargets)
	}
	ui := onboard.NewStdUI()
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			ui.Error(fmt.Sprintf("%s: the test message failed: %s", r.Target, app.Sentence(r.Err.Error())))
			failed++
			continue
		}
		ui.Success(r.Target + ": the test message was sent.")
	}
	if cfg.Notify.HealthcheckURL != "" {
		ui.Info("dbak does not test the healthcheck URL. A request changes the state of the check.")
	}
	if failed > 0 {
		return app.Exit(app.ExitGeneral, fmt.Errorf("%d of %d test messages failed. To correct the settings, run: sudo dbak notify", failed, len(results)))
	}
	return nil
}
