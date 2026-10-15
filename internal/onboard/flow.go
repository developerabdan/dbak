package onboard

import (
	"context"
	"errors"
	"os"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
)

const (
	sectionSource       = "source"
	sectionDestination  = "destination"
	sectionSchedule     = "schedule"
	sectionRetention    = "retention"
	sectionNotification = "notify"
)

func Run(ctx context.Context, env *Env) error {
	ui := env.UI
	ui.Heading("dbak setup")
	if !env.HasSystemd {
		ui.Warn("This system does not use systemd. dbak will use cron for the schedule.")
	}
	if _, err := os.Stat(env.Paths.ConfigPath); err == nil {
		return runWithExisting(ctx, env)
	}
	return runFull(ctx, newSession(env))
}

func runFull(ctx context.Context, s *session) error {
	steps := []func() error{
		func() error { return s.configureSources(ctx) },
		func() error { return s.configureDestination(ctx) },
		s.configureSchedule,
		s.configureRetention,
		func() error { return s.configureNotify(ctx) },
		s.confirmAndSave,
		func() error { s.installSchedule(ctx); return nil },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return s.offerBackup(ctx, "Run the first backup now?")
}

func runWithExisting(ctx context.Context, env *Env) error {
	ui := env.UI
	cfg, loadErr := config.Load(env.Paths.ConfigPath)
	opts := options("Change one section", "edit", "Start again from the beginning", "restart", "Cancel", "cancel")
	if loadErr != nil {
		ui.Warn(app.Sentence(loadErr.Error()))
		opts = opts[1:]
	}
	choice, err := ui.Select("A config exists in "+env.Paths.ConfigPath+". What do you want to do?", "", opts, opts[0].Value)
	if err != nil {
		return err
	}
	switch choice {
	case "cancel":
		ui.Info("dbak init stopped. The config did not change.")
		return nil
	case "restart":
		return runFull(ctx, newSession(env))
	}
	s := newSession(env)
	s.cfg = *cfg
	s.loadPasswords()
	s.telegramToken = loadSecretIfPresent(cfg.Notify.TelegramTokenFile)
	return editSection(ctx, s)
}

func editSection(ctx context.Context, s *session) error {
	section, err := s.env.UI.Select("Select the section to change.", "", options(
		"Database sources", sectionSource,
		"SFTP destination", sectionDestination,
		"Schedule", sectionSchedule,
		"Retention", sectionRetention,
		"Notifications", sectionNotification,
	), sectionSource)
	if err != nil {
		return err
	}
	switch section {
	case sectionSource:
		err = s.manageSources(ctx)
	case sectionDestination:
		err = s.configureDestination(ctx)
	case sectionSchedule:
		err = s.configureSchedule()
	case sectionRetention:
		err = s.configureRetention()
	case sectionNotification:
		err = s.configureNotify(ctx)
	}
	if err != nil {
		return err
	}
	if err := s.confirmAndSave(); err != nil {
		return err
	}
	s.installSchedule(ctx)
	return s.offerBackup(ctx, "Run a backup now?")
}

func IsAborted(err error) bool {
	return errors.Is(err, ErrAborted)
}
