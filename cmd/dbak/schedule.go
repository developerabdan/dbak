package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"dbak/internal/app"
	"dbak/internal/config"
	"dbak/internal/schedule"
	"dbak/internal/system"
)

func newScheduleCommand(opts *globalOptions) *cobra.Command {
	var show bool
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Install the schedule from the config again.",
		Long:  "Read the config and install the systemd timer or the cron file again. Use this command after you change the config manually. You must run this command as root.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			inst, err := newScheduleInstaller(opts)
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			if show {
				return showSchedule(cmd, inst, cfg)
			}
			if !system.IsRoot() {
				return app.Exit(app.ExitGeneral, errors.New("dbak schedule must run as root. Run: sudo dbak schedule"))
			}
			if inst.Owner, err = system.LookupOwner(system.ServiceUser); err != nil {
				return app.Exit(app.ExitGeneral, fmt.Errorf("the user %s does not exist. Run: sudo dbak init", system.ServiceUser))
			}
			res, err := inst.Install(cmd.Context(), cfg.Schedule)
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			out := cmd.OutOrStdout()
			for _, w := range res.Warnings {
				fmt.Fprintln(out, "WARNING: "+w)
			}
			fmt.Fprintln(out, res.Summary())
			return nil
		},
	}
	cmd.Flags().BoolVar(&show, "show", false, "Show the installed schedule and the time of the next backup.")
	return cmd
}

func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, app.Exit(app.ExitConfigInvalid, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, app.Exit(app.ExitConfigInvalid, err)
	}
	return cfg, nil
}

func newScheduleInstaller(opts *globalOptions) (*schedule.Installer, error) {
	paths := schedule.DefaultPaths()
	var err error
	if paths.ConfigPath, err = filepath.Abs(opts.configPath); err != nil {
		return nil, err
	}
	if paths.StateDir, err = filepath.Abs(opts.stateDir); err != nil {
		return nil, err
	}
	return &schedule.Installer{
		Paths:      paths,
		Systemd:    system.HasSystemd(),
		SystemZone: system.DetectTimezone(),
		Run:        schedule.RunCommand,
		LookPath:   exec.LookPath,
	}, nil
}

func showSchedule(cmd *cobra.Command, inst *schedule.Installer, cfg *config.Config) error {
	cur, err := inst.Current(cmd.Context())
	if err != nil {
		return app.Exit(app.ExitGeneral, err)
	}
	if cur.Mode == "" {
		return app.Exit(app.ExitGeneral, errors.New("no schedule is installed. Run: sudo dbak schedule"))
	}
	plan, err := schedule.NewPlan(cfg.Schedule)
	if err != nil {
		return app.Exit(app.ExitConfigInvalid, err)
	}
	out := cmd.OutOrStdout()
	if cur.Mode == schedule.ModeSystemd {
		row(out, "Scheduler", fmt.Sprintf("systemd timer %s (%s, %s)", schedule.TimerName, cur.Enabled, cur.Active))
		row(out, "OnCalendar", cur.Expression)
	} else {
		row(out, "Scheduler", "cron file "+inst.Paths.CronFile)
		row(out, "Cron time", cur.Expression)
		row(out, "Log file", inst.Paths.LogFile())
	}
	row(out, "Config", plan.Describe())
	if cfg.Schedule.RandomizedDelay != "" && cfg.Schedule.RandomizedDelay != "0" {
		row(out, "Random delay", "up to "+cfg.Schedule.RandomizedDelay)
	}
	row(out, "Next backup", nextRun(cur, plan))
	return nil
}

func nextRun(cur schedule.Installed, plan schedule.Plan) string {
	if cur.Mode == schedule.ModeSystemd {
		if cur.Next == "" || cur.Next == "n/a" {
			return "none. The timer is not active. Run: sudo dbak schedule"
		}
		return cur.Next
	}
	next := plan.Next(time.Now())
	loc, err := time.LoadLocation(plan.Zone)
	if err == nil {
		next = next.In(loc)
	}
	return schedule.FormatTime(next)
}

func row(out io.Writer, name, value string) {
	fmt.Fprintf(out, "%-13s %s\n", name+":", value)
}
