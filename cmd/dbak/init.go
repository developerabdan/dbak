package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"dbak/internal/app"
	"dbak/internal/backup"
	"dbak/internal/config"
	"dbak/internal/engine"
	"dbak/internal/onboard"
	"dbak/internal/sftp"
	"dbak/internal/state"
	"dbak/internal/system"
)

func newInitCommand(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Configure dbak with interactive questions.",
		Long:  "Configure the database source, the SFTP destination, the schedule, the retention, and the notifications. You must run this command as root.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if !system.IsRoot() {
				return app.Exit(app.ExitGeneral, errors.New("dbak init must run as root. Run: sudo dbak init"))
			}
			env, err := newInitEnv(opts)
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			if err := onboard.Run(ctx, env); err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			return nil
		},
	}
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
