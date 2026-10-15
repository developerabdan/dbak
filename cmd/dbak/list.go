package main

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/backup"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/system"
)

func newListCommand(opts *globalOptions) *cobra.Command {
	var source, database string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show the backups on the SFTP server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			targets, err := backup.SelectTargets(cfg, source, database)
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			hostname, err := system.Hostname()
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			client, err := sftp.Dial(backup.SFTPConfig(cfg.Destination))
			if err != nil {
				return app.Exit(app.ExitUploadFailed, err)
			}
			defer client.Close()
			loc, err := time.LoadLocation(cfg.Schedule.Timezone)
			if err != nil {
				loc = time.UTC
			}
			var all []backup.Entry
			for _, target := range targets {
				for _, db := range target.Databases {
					entries, err := backup.ListBackups(client, cfg.Destination.RemotePath, hostname, target.Source.Name, db)
					if err != nil {
						return app.Exit(app.ExitUploadFailed, err)
					}
					all = append(all, entries...)
				}
			}
			out := cmd.OutOrStdout()
			if len(all) == 0 {
				fmt.Fprintln(out, "The SFTP server has no backups for this host. To make a backup now, run: sudo -u dbak dbak run")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SOURCE\tDATABASE\tTIME\tSIZE\tFILE")
			for _, e := range all {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Source, e.Database, e.Time.In(loc).Format("2006-01-02 15:04 MST"), app.HumanSize(e.SizeBytes), e.Path)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "Show only the backups of this source.")
	cmd.Flags().StringVar(&database, "db", "", "Show only the backups of this database. If two sources have this database, add --source.")
	return cmd
}
