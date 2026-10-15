package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/backup"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/notify"
	"github.com/developerabdan/dbak/internal/onboard"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/state"
	"github.com/developerabdan/dbak/internal/system"
)

func newInitCommand(opts *globalOptions) *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure dbak with interactive questions.",
		Long: "Configure the database source, the SFTP destination, the schedule, the retention, and the notifications. You must run this command as root.\n\n" +
			"With --config, dbak init does not ask questions. It reads the file, examines the database and the SFTP server, saves the config in " + app.ConfigPath + ", and installs the schedule. " +
			"Give the database password in DBAK_DB_PASSWORD and the Telegram bot token in DBAK_TELEGRAM_TOKEN, or put them in the files that the config names.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if !system.IsRoot() {
				return app.Exit(app.ExitGeneral, errors.New("dbak init must run as root. Run: sudo dbak init"))
			}
			secrets := onboard.Secrets{DBPassword: os.Getenv("DBAK_DB_PASSWORD"), TelegramToken: os.Getenv("DBAK_TELEGRAM_TOKEN")}
			os.Unsetenv("DBAK_DB_PASSWORD")
			os.Unsetenv("DBAK_TELEGRAM_TOKEN")
			env, err := newInitEnv(opts)
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			if inputFile != "" {
				env.UI = onboard.NewPlainUI(strings.NewReader(""), cmd.OutOrStdout(), nil)
				return onboard.RunFromFile(ctx, env, inputFile, secrets)
			}
			if err := onboard.Run(ctx, env); err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "config", "", "Read the settings from this file and do not ask questions.")
	return cmd
}

func newInitEnv(opts *globalOptions) (*onboard.Env, error) {
	owner, err := system.EnsureServiceUser()
	if err != nil {
		return nil, err
	}
	if err := owner.MkdirAll(opts.stateDir, 0o750); err != nil {
		return nil, err
	}
	hostname, err := system.Hostname()
	if err != nil {
		return nil, err
	}
	release, _ := system.ReadOSRelease("/etc/os-release")
	installer, err := newScheduleInstaller(opts)
	if err != nil {
		return nil, err
	}
	installer.Owner = owner
	return &onboard.Env{
		UI:         onboard.NewStdUI(),
		Paths:      onboard.Paths{ConfigPath: opts.configPath, StateDir: opts.stateDir},
		Owner:      owner,
		Hostname:   hostname,
		Timezone:   system.DetectTimezone(),
		PrimaryIP:  system.PrimaryIP(),
		OSFamily:   release.Family(),
		HasSystemd: system.HasSystemd(),
		NewEngine: func(source config.Source, password string) (engine.Full, error) {
			return backup.NewEngine(source, password, opts.stateDir)
		},
		LookPath:   exec.LookPath,
		RunCommand: runVisible,
		DialSFTP: func(cfg sftp.Config) (onboard.SFTPClient, error) {
			client, err := sftp.Dial(cfg)
			if err != nil {
				return nil, err
			}
			return client, nil
		},
		FetchHostKey: func(host string, port int) (ssh.PublicKey, error) {
			return sftp.FetchHostKey(host, port, 15*time.Second)
		},
		RunBackup: func(ctx context.Context) (state.Run, error) {
			return backup.RunJob(ctx, backup.JobOptions{
				ConfigPath: opts.configPath,
				StateDir:   opts.stateDir,
				Logger:     progressLogger(),
			})
		},
		InstallSchedule: installer.Install,
		SendTelegramTest: func(ctx context.Context, token, chatID string) error {
			n := notify.New(config.Notify{TelegramChatID: chatID}, token, slog.New(slog.DiscardHandler))
			return n.SendTelegram(ctx, notify.TelegramTestText(hostname))
		},
	}, nil
}

func runVisible(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func progressLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey) {
				return slog.Attr{}
			}
			return a
		},
	}))
}

func newUserHelperCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "user-helper",
		Short: "Show the SQL that makes a read-only database user for backup.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := onboard.RunUserHelper(onboard.NewStdUI(), "", system.PrimaryIP())
			if onboard.IsAborted(err) {
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Use the user name %s and the password from the SQL in dbak init.\n", result.User)
			return nil
		},
	}
}
