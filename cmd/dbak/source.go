package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/onboard"
	"github.com/developerabdan/dbak/internal/system"
)

func newSourceCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Show, add, change, or remove the database sources.",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "Show the database sources in the config.",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, err := loadConfig(opts.configPath)
				if err != nil {
					return err
				}
				return writeSources(cmd.OutOrStdout(), cfg)
			},
		},
		&cobra.Command{
			Use:   "add",
			Short: "Add a database source with interactive questions.",
			Long:  "Add a database source with interactive questions. dbak examines the connection and the dump check, saves the config, and can make a backup of the new source. You must run this command as root.",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runOnboarding(cmd, opts, "source add", func(ctx context.Context, env *onboard.Env) error {
					return onboard.AddSource(ctx, env)
				})
			},
		},
		&cobra.Command{
			Use:   "edit NAME",
			Short: "Change a database source with interactive questions.",
			Long:  "Change the engine, the name, the connection, or the databases of a source. You must run this command as root.",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runOnboarding(cmd, opts, "source edit", func(ctx context.Context, env *onboard.Env) error {
					return onboard.EditSource(ctx, env, args[0])
				})
			},
		},
		&cobra.Command{
			Use:   "remove NAME",
			Short: "Remove a database source from the config.",
			Long:  "Remove a database source and its password file. The backups on the SFTP server stay. You must run this command as root.",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runOnboarding(cmd, opts, "source remove", func(_ context.Context, env *onboard.Env) error {
					return onboard.RemoveSource(env, args[0])
				})
			},
		},
	)
	return cmd
}

func runOnboarding(cmd *cobra.Command, opts *globalOptions, name string, run func(ctx context.Context, env *onboard.Env) error) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !system.IsRoot() {
		return app.Exit(app.ExitGeneral, fmt.Errorf("dbak %s must run as root. Run: sudo dbak %s", name, strings.Join(os.Args[1:], " ")))
	}
	env, err := newInitEnv(opts)
	if err != nil {
		return app.Exit(app.ExitGeneral, err)
	}
	err = run(ctx, env)
	if onboard.IsAborted(err) {
		fmt.Fprintf(cmd.OutOrStdout(), "dbak %s stopped. The config did not change.\n", name)
		return nil
	}
	if err != nil {
		return app.Exit(app.ExitGeneral, err)
	}
	return nil
}

func writeSources(out io.Writer, cfg *config.Config) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tENGINE\tSERVER\tUSER\tDATABASES\tRETENTION")
	for _, src := range cfg.Sources {
		retention := "global"
		if src.Retention != nil {
			r := src.Retention
			retention = fmt.Sprintf("%d daily, %d weekly, %d monthly", r.Daily, r.Weekly, r.Monthly)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s:%d\t%s\t%s\t%s\n", src.Name, src.Engine, src.Host, src.Port, src.Username, strings.Join(src.Databases, ", "), retention)
	}
	return tw.Flush()
}
