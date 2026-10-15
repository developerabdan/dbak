package main

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/backup"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/doctor"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/schedule"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/system"
)

func newDoctorCommand(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Examine the config, the permissions, the connections, and the schedule.",
		Long:  "Examine the config, the file permissions, the client tool, the database connection, the dump check, the SFTP connection, the schedule, and the clock. Run this command with sudo to examine all items.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := newDoctorEnv(opts)
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			results := doctor.Run(cmd.Context(), env)
			printResults(cmd.OutOrStdout(), results)
			if failed := doctor.Failed(results); failed > 0 {
				return app.Exit(app.ExitGeneral, fmt.Errorf("%s failed. Do the fixes above, then run dbak doctor again", count(failed, "check")))
			}
			return nil
		},
	}
}

func newDoctorEnv(opts *globalOptions) (*doctor.Env, error) {
	hostname, err := system.Hostname()
	if err != nil {
		return nil, err
	}
	inst, err := newScheduleInstaller(opts)
	if err != nil {
		return nil, err
	}
	env := &doctor.Env{
		ConfigPath: inst.Paths.ConfigPath,
		StateDir:   inst.Paths.StateDir,
		Hostname:   hostname,
		NewEngine: func(source config.Source, password string) (engine.Full, error) {
			return backup.NewEngine(source, password, opts.stateDir)
		},
		DialSFTP: func(cfg sftp.Config) (doctor.SFTPClient, error) {
			client, err := sftp.Dial(cfg)
			if err != nil {
				return nil, err
			}
			return client, nil
		},
		Schedule:    func(ctx context.Context) (schedule.Installed, error) { return inst.Current(ctx) },
		CronRunning: func() bool { return system.ProcessRunning("cron", "crond") },
		Run:         schedule.RunCommand,
	}
	if owner, err := system.LookupOwner(system.ServiceUser); err == nil {
		env.Owner = &owner
	}
	return env, nil
}

func printResults(out io.Writer, results []doctor.Result) {
	renderer := lipgloss.NewRenderer(out)
	styles := map[doctor.Level]lipgloss.Style{
		doctor.OK:   renderer.NewStyle().Foreground(lipgloss.Color("2")).Bold(true),
		doctor.Warn: renderer.NewStyle().Foreground(lipgloss.Color("3")).Bold(true),
		doctor.Fail: renderer.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
	}
	dim := renderer.NewStyle().Faint(true)
	warnings := 0
	for _, r := range results {
		label := styles[r.Level].Render(fmt.Sprintf("%-4s", r.Level))
		fmt.Fprintf(out, "%s  %s: %s\n", label, r.Name, r.Message)
		if r.Fix != "" {
			fmt.Fprintf(out, "      %s\n", dim.Render(r.Fix))
		}
		if r.Level == doctor.Warn {
			warnings++
		}
	}
	failed := doctor.Failed(results)
	fmt.Fprintf(out, "\n%s, %s failed, %s.\n", count(len(results), "check"), count(failed, ""), count(warnings, "warning"))
}

func count(n int, noun string) string {
	if noun == "" {
		return fmt.Sprint(n)
	}
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
