package backup

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"time"

	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/retention"
	"github.com/developerabdan/dbak/internal/state"
)

type DirStore interface {
	ReadDir(dir string) ([]os.FileInfo, error)
	Remove(path string) error
}

type Retention struct {
	Store      DirStore
	RemotePath string
	Hostname   string
	Rules      retention.Rules
	Location   *time.Location
	Logger     *slog.Logger
	Now        func() time.Time
}

func NewRetention(cfg *config.Config, store DirStore, hostname string, logger *slog.Logger) (*Retention, error) {
	loc, err := time.LoadLocation(cfg.Schedule.Timezone)
	if err != nil {
		return nil, fmt.Errorf("the timezone %q is not known", cfg.Schedule.Timezone)
	}
	return &Retention{
		Store:      store,
		RemotePath: cfg.Destination.RemotePath,
		Hostname:   hostname,
		Rules: retention.Rules{
			Daily:     cfg.Retention.Daily,
			Weekly:    cfg.Retention.Weekly,
			Monthly:   cfg.Retention.Monthly,
			KeepToday: cfg.Schedule.Frequency == config.FrequencyEveryNHours,
		},
		Location: loc,
		Logger:   logger,
		Now:      time.Now,
	}, nil
}

func (r *Retention) Apply(results []Result) {
	for _, res := range results {
		if res.Status != state.StatusSuccess {
			continue
		}
		removed, err := r.applyOne(res.Name)
		if err != nil {
			r.Logger.Warn(fmt.Sprintf("Retention of %s stopped. The backup result does not change.", res.Name), "database", res.Name, "error", err.Error())
		}
		if removed > 0 {
			r.Logger.Info(fmt.Sprintf("Retention removed %d old files of %s.", removed, res.Name), "database", res.Name, "removed", removed)
		}
	}
}

func (r *Retention) applyOne(database string) (int, error) {
	dir := RemoteDir(r.RemotePath, r.Hostname, database)
	entries, err := r.Store.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var files []retention.File
	for _, entry := range entries {
		if !entry.Mode().IsRegular() {
			continue
		}
		if f, ok := retention.Parse(database, entry.Name()); ok {
			files = append(files, f)
		}
	}
	removed := 0
	for _, f := range retention.Remove(files, r.Rules, r.Location, r.Now()) {
		target := path.Join(dir, f.Name)
		if err := r.Store.Remove(target); err != nil {
			return removed, err
		}
		r.Logger.Info("Retention removed a file.", "database", database, "remote_path", target)
		removed++
	}
	return removed, nil
}
