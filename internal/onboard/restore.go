package onboard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"filippo.io/age"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/backup"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/encrypt"
	"github.com/developerabdan/dbak/internal/restore"
)

func Restore(ctx context.Context, env *Env) error {
	cfg, err := existingConfig(env)
	if err != nil {
		return err
	}
	ui := env.UI
	ui.Heading("Restore a backup")
	source, err := selectRestoreSource(ui, cfg)
	if err != nil {
		return err
	}
	store, err := env.OpenBackups(backup.SFTPConfig(cfg.Destination))
	if err != nil {
		return err
	}
	defer store.Close()
	entries, err := sourceBackups(store, cfg, env.Hostname, source)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("the SFTP server has no backups of the source %s. To make a backup now, run: sudo -u dbak dbak run --source %s", source.Name, source.Name)
	}
	entry, err := selectBackup(ui, entries, scheduleLocation(cfg))
	if err != nil {
		return err
	}
	dir, err := ui.Input("Directory for the backup file",
		"dbak writes the file to this directory. If the directory does not exist, dbak makes it.",
		env.RestoreDir, func(dir string) error { return restore.CheckTarget(dir, entry.Path) })
	if err != nil {
		return err
	}
	var identity age.Identity
	if restore.Encrypted(entry.Name) {
		if identity, err = askIdentity(ui); err != nil {
			return err
		}
	}
	req := restore.Request{RemoteFile: entry.Path, Dir: dir, Identity: identity, Owner: env.RestoreOwner}
	ui.Info(fmt.Sprintf("Download of %s started.", entry.Name))
	res, err := restore.Download(ctx, store, req)
	for errors.Is(err, encrypt.ErrWrongKey) {
		ui.Error(app.Sentence(err.Error()))
		if req.Identity, err = askIdentity(ui); err != nil {
			return err
		}
		res, err = restore.Download(ctx, store, req)
	}
	if err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("dbak wrote the backup to %s (%s).", res.Path, app.HumanSize(res.SizeBytes)))
	return nil
}

func selectRestoreSource(ui UI, cfg *config.Config) (config.Source, error) {
	if len(cfg.Sources) == 1 {
		ui.Info(fmt.Sprintf("Source: %s (%s)", cfg.Sources[0].Name, cfg.Sources[0].Engine))
		return cfg.Sources[0], nil
	}
	opts := make([]Option, 0, len(cfg.Sources))
	for _, src := range cfg.Sources {
		opts = append(opts, Option{Label: fmt.Sprintf("%s (%s, %s:%d)", src.Name, src.Engine, src.Host, src.Port), Value: src.Name})
	}
	name, err := ui.Select("Select the source", "", opts, opts[0].Value)
	if err != nil {
		return config.Source{}, err
	}
	src, _ := cfg.SourceByName(name)
	return *src, nil
}

func sourceBackups(store BackupStore, cfg *config.Config, hostname string, source config.Source) ([]backup.Entry, error) {
	var all []backup.Entry
	for _, db := range source.Databases {
		entries, err := backup.ListBackups(store, cfg.Destination.RemotePath, hostname, source.Name, db)
		if err != nil {
			return nil, fmt.Errorf("cannot read the backups of %s on the SFTP server: %w", db, err)
		}
		all = append(all, entries...)
	}
	slices.SortStableFunc(all, func(a, b backup.Entry) int { return b.Time.Compare(a.Time) })
	return all, nil
}

func selectBackup(ui UI, entries []backup.Entry, loc *time.Location) (backup.Entry, error) {
	opts := make([]Option, 0, len(entries))
	for i, e := range entries {
		opts = append(opts, Option{Label: backupLabel(e, loc), Value: fmt.Sprint(i)})
	}
	choice, err := ui.Select("Select the backup to restore", "The newest backup is first.", opts, opts[0].Value)
	if err != nil {
		return backup.Entry{}, err
	}
	for i, o := range opts {
		if o.Value == choice {
			return entries[i], nil
		}
	}
	return entries[0], nil
}

func backupLabel(e backup.Entry, loc *time.Location) string {
	label := fmt.Sprintf("%s  %s  %s", e.Time.In(loc).Format("2006-01-02 15:04 MST"), e.Database, app.HumanSize(e.SizeBytes))
	if restore.Encrypted(e.Name) {
		label += "  encrypted"
	}
	return label
}

func scheduleLocation(cfg *config.Config) *time.Location {
	loc, err := time.LoadLocation(cfg.Schedule.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func askIdentity(ui UI) (age.Identity, error) {
	ui.Info("The backup is encrypted. dbak uses the private key only for this restore. dbak does not save the key.")
	key, err := ui.Secret("Paste the age private key", "The key starts with AGE-SECRET-KEY-1.", func(v string) error {
		_, err := encrypt.ParseIdentity(v)
		return err
	})
	if err != nil {
		return nil, err
	}
	return encrypt.ParseIdentity(key)
}
