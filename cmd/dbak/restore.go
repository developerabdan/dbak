package main

import (
	"context"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/developerabdan/dbak/internal/onboard"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/system"
)

const restoreDirName = "backups"

func newRestoreCommand(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Download a backup from the SFTP server to a local directory.",
		Long:  "Download a backup from the SFTP server to a local directory with interactive questions. dbak decrypts an encrypted backup with the age private key that you type, and decompresses a MySQL dump to a .sql file. dbak does not change a database. You must run this command as root.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOnboarding(cmd, opts, "restore", func(ctx context.Context, env *onboard.Env) error {
				home, owner, err := system.InvokingUser()
				if err != nil {
					return err
				}
				env.RestoreDir = filepath.Join(home, restoreDirName)
				env.RestoreOwner = owner
				env.OpenBackups = func(cfg sftp.Config) (onboard.BackupStore, error) {
					return sftp.Dial(cfg)
				}
				return onboard.Restore(ctx, env)
			})
		},
	}
}
