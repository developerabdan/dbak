package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/developerabdan/dbak/internal/app"
)

type globalOptions struct {
	configPath string
	stateDir   string
}

func newRootCommand() *cobra.Command {
	opts := &globalOptions{}
	root := &cobra.Command{
		Use:           app.Name,
		Short:         "Make backups of PostgreSQL and MySQL databases and send them to an SFTP server.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&opts.configPath, "config", app.ConfigPath, "Path of the config file.")
	root.PersistentFlags().StringVar(&opts.stateDir, "state-dir", app.StateDir, "Directory for the lock file, the run history, and temporary credential files.")
	root.PersistentFlags().BoolP("help", "h", false, "Show the help for the command.")
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(newInitCommand(opts), newRunCommand(opts), newScheduleCommand(opts), newStatusCommand(opts), newListCommand(opts), newUserHelperCommand(), newVersionCommand())
	root.InitDefaultHelpCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "help" {
			cmd.Short = "Show the help for a command."
		}
	}
	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the version, the commit, and the build date.",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (commit %s, built %s)\n", app.Name, app.Version, app.Commit, app.Date)
		},
	}
}
