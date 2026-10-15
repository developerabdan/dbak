package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/onboard"
	"github.com/developerabdan/dbak/internal/system"
	"github.com/developerabdan/dbak/internal/uninstall"
)

func newUninstallCommand(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the schedule and the binary from this server.",
		Long:  "Remove the systemd timer or the cron file, and the binary. dbak asks before it removes the config, the secrets, and the run history. The backups on the SFTP server stay. You must run this command as root.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !system.IsRoot() {
				return app.Exit(app.ExitGeneral, errors.New("dbak uninstall must run as root. Run: sudo dbak uninstall"))
			}
			err := runUninstall(cmd, opts, onboard.NewStdUI())
			if onboard.IsAborted(err) {
				fmt.Fprintln(cmd.OutOrStdout(), "dbak uninstall stopped. Nothing changed.")
				return nil
			}
			if err != nil {
				return app.Exit(app.ExitGeneral, err)
			}
			return nil
		},
	}
}

func runUninstall(cmd *cobra.Command, opts *globalOptions, ui onboard.UI) error {
	ok, err := ui.Confirm("Remove dbak from this server?", "dbak removes the schedule and the binary. The backups on the SFTP server stay.", false)
	if err != nil {
		return err
	}
	if !ok {
		return onboard.ErrAborted
	}
	inst, err := newScheduleInstaller(opts)
	if err != nil {
		return err
	}
	data := uninstall.Data{ConfigPath: inst.Paths.ConfigPath, StateDir: inst.Paths.StateDir, LogDir: inst.Paths.LogDir}
	purge, err := ui.Confirm("Remove the config, the secrets, and the run history too?",
		"dbak removes the dbak files in "+strings.Join(data.Directories(), ", ")+". Select No to keep them for a new install.", false)
	if err != nil {
		return err
	}
	removed, err := inst.Remove(cmd.Context())
	for _, file := range removed {
		ui.Success("Removed " + file + ".")
	}
	if err != nil {
		return err
	}
	if purge {
		files, err := uninstall.RemoveData(data)
		for _, file := range files {
			ui.Success("Removed " + file + ".")
		}
		if err != nil {
			return err
		}
	}
	if err := os.Remove(inst.Paths.Binary); err == nil {
		ui.Success("Removed " + inst.Paths.Binary + ".")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot remove %s: %w", inst.Paths.Binary, err)
	}
	ui.Info(fmt.Sprintf("dbak is removed. The backups on the SFTP server stay. To remove the system user, run: sudo userdel %s", system.ServiceUser))
	if !purge {
		ui.Info("The config and the secrets stay in " + data.Directories()[0] + ".")
	}
	return nil
}
