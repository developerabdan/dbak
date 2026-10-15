package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"dbak/internal/backup"
)

func newRunCommand(opts *globalOptions) *cobra.Command {
	var database string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Make a backup of the databases now and upload it to the SFTP server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			logger := slog.New(slog.NewTextHandler(cmd.OutOrStdout(), nil))
			_, err := backup.RunJob(ctx, backup.JobOptions{
				ConfigPath: opts.configPath,
				StateDir:   opts.stateDir,
				Database:   database,
				Logger:     logger,
			})
			return err
		},
	}
	cmd.Flags().StringVar(&database, "db", "", "Make a backup of only this database. The database must be in the config.")
	return cmd
}
