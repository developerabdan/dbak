package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"dbak/internal/app"
	"dbak/internal/backup"
	"dbak/internal/config"
	"dbak/internal/engine"
	"dbak/internal/engine/mysql"
	"dbak/internal/engine/postgres"
	"dbak/internal/lock"
	"dbak/internal/sftp"
	"dbak/internal/state"
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
			return runBackup(ctx, opts, database, logger)
		},
	}
	cmd.Flags().StringVar(&database, "db", "", "Make a backup of only this database. The database must be in the config.")
	return cmd
}

func runBackup(ctx context.Context, opts *globalOptions, database string, logger *slog.Logger) error {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return app.Exit(app.ExitConfigInvalid, err)
	}
	databases, err := selectDatabases(cfg.Source.Databases, database)
	if err != nil {
		return app.Exit(app.ExitGeneral, err)
	}
	runLock, err := lock.Acquire(filepath.Join(opts.stateDir, app.LockFile))
	if errors.Is(err, lock.ErrLocked) {
		logger.Warn("A different dbak run is in progress. This run stopped.")
		return app.Exit(app.ExitLocked, err)
	}
	if err != nil {
		return app.Exit(app.ExitGeneral, err)
	}
	defer runLock.Release()

	password, err := config.ReadSecret(cfg.Source.PasswordFile)
	if err != nil {
		return app.Exit(app.ExitConfigInvalid, err)
	}
	eng, err := newEngine(cfg.Source, password, opts.stateDir)
	if err != nil {
		return app.Exit(app.ExitConfigInvalid, err)
	}
	info, err := eng.Probe(ctx, databases[0])
	if err != nil {
		return app.Exit(app.ExitDatabase, err)
	}
	logger.Info("Connected to the database server.", "engine", info.Flavor, "version", info.Version)

	hostname, err := os.Hostname()
	if err != nil {
		return app.Exit(app.ExitGeneral, fmt.Errorf("cannot get the host name: %w", err))
	}
	client, err := sftp.Dial(sftpConfig(cfg.Destination))
	if err != nil {
		return app.Exit(app.ExitUploadFailed, err)
	}
	defer client.Close()

	started := time.Now().UTC()
	runner := &backup.Runner{
		Engine:     eng,
		Store:      client,
		RemotePath: cfg.Destination.RemotePath,
		Hostname:   hostname,
		Logger:     logger,
	}
	results := runner.Run(ctx, databases)
	record := state.Run{
		Host:       hostname,
		StartedAt:  started,
		FinishedAt: time.Now().UTC(),
		Databases:  backup.Databases(results),
	}
	record.Status = state.OverallStatus(record.Databases)
	if err := state.Append(filepath.Join(opts.stateDir, app.StateFile), record); err != nil {
		logger.Warn("Cannot save the run history. The backup result does not change.", "error", err.Error())
	}
	logger.Info("Backup run completed.", "status", record.Status, "databases", len(results))
	return runError(results)
}

func runError(results []backup.Result) error {
	code := backup.ExitCode(results)
	if code == app.ExitOK {
		return nil
	}
	failed := 0
	for _, res := range results {
		if res.Status != state.StatusSuccess {
			failed++
		}
	}
	return app.Exit(code, fmt.Errorf("the backup of %d of %d databases failed. Examine the log for the cause", failed, len(results)))
}

func selectDatabases(configured []string, only string) ([]string, error) {
	if only == "" {
		return configured, nil
	}
	if !slices.Contains(configured, only) {
		return nil, fmt.Errorf("the database %s is not in source.databases. Use a database from the config", only)
	}
	return []string{only}, nil
}

func newEngine(source config.Source, password, tempDir string) (engine.Engine, error) {
	opts := engine.Options{
		Host:     source.Host,
		Port:     source.Port,
		Username: source.Username,
		Password: password,
		SSLMode:  source.SSLMode,
		TempDir:  tempDir,
	}
	switch source.Engine {
	case config.EnginePostgres:
		return postgres.New(opts), nil
	case config.EngineMySQL:
		return mysql.New(opts), nil
	}
	return nil, fmt.Errorf("the engine %q is not known. Use postgres or mysql", source.Engine)
}

func sftpConfig(d config.Destination) sftp.Config {
	return sftp.Config{
		Host:           d.Host,
		Port:           d.Port,
		Username:       d.Username,
		PrivateKeyFile: d.PrivateKeyFile,
		KnownHostsFile: d.KnownHostsFile,
	}
}
