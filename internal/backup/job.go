package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/engine/mysql"
	"github.com/developerabdan/dbak/internal/engine/postgres"
	"github.com/developerabdan/dbak/internal/lock"
	"github.com/developerabdan/dbak/internal/notify"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/state"
)

type JobOptions struct {
	ConfigPath string
	StateDir   string
	Database   string
	Logger     *slog.Logger
}

func RunJob(ctx context.Context, opts JobOptions) (state.Run, error) {
	logger := opts.Logger
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return state.Run{}, app.Exit(app.ExitConfigInvalid, err)
	}
	databases, err := SelectDatabases(cfg.Source.Databases, opts.Database)
	if err != nil {
		return state.Run{}, app.Exit(app.ExitGeneral, err)
	}
	runLock, err := lock.Acquire(filepath.Join(opts.StateDir, app.LockFile))
	if errors.Is(err, lock.ErrLocked) {
		logger.Warn("A different dbak run is in progress. This run stopped.")
		return state.Run{}, app.Exit(app.ExitLocked, err)
	}
	if err != nil {
		return state.Run{}, app.Exit(app.ExitGeneral, err)
	}
	defer runLock.Release()

	hostname, err := os.Hostname()
	if err != nil {
		return state.Run{}, app.Exit(app.ExitGeneral, fmt.Errorf("cannot get the host name: %w", err))
	}
	notifier := newNotifier(cfg.Notify, logger)
	notifier.Start(ctx)
	started := time.Now().UTC()
	results, err := backupDatabases(ctx, cfg, databases, hostname, opts)
	if err != nil {
		results = failAll(databases, err)
	}
	record := state.Run{
		Host:       hostname,
		StartedAt:  started,
		FinishedAt: time.Now().UTC(),
		Databases:  Databases(results),
	}
	record.Status = state.OverallStatus(record.Databases)
	if err := state.Append(filepath.Join(opts.StateDir, app.StateFile), record); err != nil {
		logger.Warn("Cannot save the run history. The backup result does not change.", "error", err.Error())
	}
	logger.Info("Backup run completed.", "status", record.Status, "databases", len(results))
	notifier.Finish(ctx, record)
	if err != nil {
		return record, err
	}
	return record, runError(results)
}

func newNotifier(cfg config.Notify, logger *slog.Logger) *notify.Notifier {
	token := ""
	if cfg.TelegramTokenFile != "" && cfg.TelegramChatID != "" {
		var err error
		if token, err = config.ReadSecret(cfg.TelegramTokenFile); err != nil {
			logger.Warn("Cannot read the Telegram bot token. dbak will not send Telegram notifications.", "error", err.Error())
		}
	}
	return notify.New(cfg, token, logger)
}

func backupDatabases(ctx context.Context, cfg *config.Config, databases []string, hostname string, opts JobOptions) ([]Result, error) {
	logger := opts.Logger
	password, err := config.ReadSecret(cfg.Source.PasswordFile)
	if err != nil {
		return nil, app.Exit(app.ExitConfigInvalid, err)
	}
	eng, err := NewEngine(cfg.Source, password, opts.StateDir)
	if err != nil {
		return nil, app.Exit(app.ExitConfigInvalid, err)
	}
	info, err := eng.Probe(ctx, databases[0])
	if err != nil {
		return nil, app.Exit(app.ExitDatabase, err)
	}
	logger.Info("Connected to the database server.", "engine", info.Flavor, "version", info.Version)

	client, err := sftp.Dial(SFTPConfig(cfg.Destination))
	if err != nil {
		return nil, app.Exit(app.ExitUploadFailed, err)
	}
	defer client.Close()

	runner := &Runner{
		Engine:     eng,
		Store:      client,
		RemotePath: cfg.Destination.RemotePath,
		Hostname:   hostname,
		Logger:     logger,
	}
	results := runner.Run(ctx, databases)
	keeper, err := NewRetention(cfg, client, hostname, logger)
	if err != nil {
		logger.Warn("Retention did not run. The backup result does not change.", "error", err.Error())
		return results, nil
	}
	keeper.Apply(results)
	return results, nil
}

func failAll(databases []string, err error) []Result {
	results := make([]Result, 0, len(databases))
	for _, name := range databases {
		results = append(results, Result{Database: state.Database{Name: name, Status: state.StatusFailure, Error: err.Error()}})
	}
	return results
}

func runError(results []Result) error {
	code := ExitCode(results)
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

func SelectDatabases(configured []string, only string) ([]string, error) {
	if only == "" {
		return configured, nil
	}
	if !slices.Contains(configured, only) {
		return nil, fmt.Errorf("the database %s is not in source.databases. Use a database from the config", only)
	}
	return []string{only}, nil
}

func NewEngine(source config.Source, password, tempDir string) (engine.Full, error) {
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

func SFTPConfig(d config.Destination) sftp.Config {
	return sftp.Config{
		Host:           d.Host,
		Port:           d.Port,
		Username:       d.Username,
		PrivateKeyFile: d.PrivateKeyFile,
		KnownHostsFile: d.KnownHostsFile,
	}
}
