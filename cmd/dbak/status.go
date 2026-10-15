package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/state"
	"github.com/developerabdan/dbak/internal/status"
)

func newStatusCommand(opts *globalOptions) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the last backup of each database and the next scheduled backup.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			history, err := state.Load(filepath.Join(opts.stateDir, app.StateFile))
			if err != nil {
				return app.Exit(app.ExitGeneral, fmt.Errorf("%w. Run the command with sudo", err))
			}
			inst, err := newScheduleInstaller(opts)
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			installed, err := inst.Current(cmd.Context())
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			report, err := status.Build(cfg, history, installed, time.Now())
			if err != nil {
				return app.Exit(app.ExitConfigInvalid, err)
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			return report.WriteText(cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Show the status as JSON.")
	return cmd
}
