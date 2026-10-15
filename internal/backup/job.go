package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	Source     string
	Database   string
	Logger     *slog.Logger
}

type Target struct {
	Source    config.Source
	Databases []string
}

func RunJob(ctx context.Context, opts JobOptions) (state.Run, error) {
	logger := opts.Logger
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return state.Run{}, app.Exit(app.ExitConfigInvalid, err)
	}
	targets, err := SelectTargets(cfg, opts.Source, opts.Database)
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
	results, err := backupTargets(ctx, cfg, targets, hostname, opts)
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

func backupTargets(ctx context.Context, cfg *config.Config, targets []Target, hostname string, opts JobOptions) ([]Result, error) {
	var client *sftp.Client
	defer func() {
		if client != nil {
			client.Close()
		}
	}()
	var results []Result
	for i, target := range targets {
		logger := opts.Logger.With("source", target.Source.Name)
		eng, failed := openSource(ctx, target, opts.StateDir, logger)
		if failed != nil {
			results = append(results, failed...)
			continue
		}
		if client == nil {
			var err error
			if client, err = sftp.Dial(SFTPConfig(cfg.Destination)); err != nil {
				results = append(results, failTargets(targets[i:], err, FailureUpload)...)
				return results, app.Exit(app.ExitUploadFailed, err)
			}
		}
		results = append(results, backupSource(ctx, cfg, client, eng, target, hostname, logger)...)
	}
	return results, nil
}

func openSource(ctx context.Context, target Target, stateDir string, logger *slog.Logger) (engine.Engine, []Result) {
	name := target.Source.Name
	logger.Info(fmt.Sprintf("Backup of the source %s started.", name))
	eng, err := sourceEngine(target.Source, stateDir)
	if err != nil {
		logger.Error(fmt.Sprintf("dbak cannot use the source %s. dbak continues with the next source.", name), "error", err.Error())
		return nil, failTarget(target, err, FailureConfig)
	}
	info, err := eng.Probe(ctx, target.Databases[0])
	if err != nil {
		logger.Error(fmt.Sprintf("Cannot connect to the database server of the source %s. dbak continues with the next source.", name), "error", err.Error())
		return nil, failTarget(target, err, FailureConnect)
	}
	logger.Info("Connected to the database server.", "engine", info.Flavor, "version", info.Version)
	return eng, nil
}

func backupSource(ctx context.Context, cfg *config.Config, client *sftp.Client, eng engine.Engine, target Target, hostname string, logger *slog.Logger) []Result {
	runner := &Runner{
		Engine:     eng,
		Store:      client,
		RemotePath: cfg.Destination.RemotePath,
		Hostname:   hostname,
		Source:     target.Source.Name,
		Logger:     logger,
	}
	results := runner.Run(ctx, target.Databases)
	keeper, err := NewRetention(cfg, target.Source, client, hostname, logger)
	if err != nil {
		logger.Warn("Retention did not run. The backup result does not change.", "error", err.Error())
		return results
	}
	keeper.Apply(results)
	return results
}

func sourceEngine(source config.Source, stateDir string) (engine.Engine, error) {
	password, err := config.ReadSecret(source.PasswordFile)
	if err != nil {
		return nil, err
	}
	return NewEngine(source, password, stateDir)
}

func failTargets(targets []Target, err error, kind string) []Result {
	var results []Result
	for _, target := range targets {
		results = append(results, failTarget(target, err, kind)...)
	}
	return results
}

func failTarget(target Target, err error, kind string) []Result {
	results := make([]Result, 0, len(target.Databases))
	for _, name := range target.Databases {
		results = append(results, Result{
			Database:    state.Database{Source: target.Source.Name, Name: name, Status: state.StatusFailure, Error: err.Error()},
			FailureKind: kind,
		})
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

func SelectTargets(cfg *config.Config, sourceName, database string) ([]Target, error) {
	sources := cfg.Sources
	if sourceName != "" {
		source, ok := cfg.SourceByName(sourceName)
		if !ok {
			return nil, fmt.Errorf("the source %s is not in the config. Use one of these sources: %s", sourceName, strings.Join(SourceNames(cfg), ", "))
		}
		sources = []config.Source{*source}
	}
	if database == "" {
		targets := make([]Target, 0, len(sources))
		for _, s := range sources {
			targets = append(targets, Target{Source: s, Databases: s.Databases})
		}
		return targets, nil
	}
	var targets []Target
	for _, s := range sources {
		if slices.Contains(s.Databases, database) {
			targets = append(targets, Target{Source: s, Databases: []string{database}})
		}
	}
	switch {
	case len(targets) == 0 && sourceName != "":
		return nil, fmt.Errorf("the database %s is not in the source %s. Use a database from the config", database, sourceName)
	case len(targets) == 0:
		return nil, fmt.Errorf("the database %s is not in the config. Use a database from the config", database)
	case len(targets) > 1:
		names := make([]string, 0, len(targets))
		for _, t := range targets {
			names = append(names, t.Source.Name)
		}
		return nil, fmt.Errorf("the database %s is in more than one source: %s. Add --source to select one source", database, strings.Join(names, ", "))
	}
	return targets, nil
}

func SourceNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Sources))
	for _, s := range cfg.Sources {
		names = append(names, s.Name)
	}
	return names
}

func NewEngine(source config.Source, password, tempDir string) (engine.Full, error) {
	opts := engine.Options{
		Host:     source.Host,
		Port:     source.Port,
		Username: source.Username,
		Password: password,
		SSLMode:  source.SSLMode,
		TempDir:  tempDir,
		Binary:   ClientTool(source),
	}
	switch source.Engine {
	case config.EnginePostgres:
		return postgres.New(opts), nil
	case config.EngineMySQL:
		return mysql.New(opts), nil
	}
	return nil, fmt.Errorf("the engine %q is not known. Use postgres or mysql", source.Engine)
}

func ClientTool(source config.Source) string {
	tool := postgres.DefaultBinary
	if source.Engine == config.EngineMySQL {
		tool = mysql.DefaultBinary
	}
	if source.BinDir == "" {
		return tool
	}
	return filepath.Join(source.BinDir, tool)
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
