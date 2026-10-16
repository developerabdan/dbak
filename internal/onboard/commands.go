package onboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/developerabdan/dbak/internal/config"
)

func sessionFor(env *Env, cfg *config.Config) *session {
	s := newSession(env)
	s.cfg = *cfg
	s.loadPasswords()
	s.telegramToken = loadSecretIfPresent(cfg.Notify.TelegramTokenFile)
	return s
}

func existingConfig(env *Env) (*config.Config, error) {
	if _, err := os.Stat(env.Paths.ConfigPath); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("dbak has no config in %s. Run: sudo dbak init", env.Paths.ConfigPath)
	}
	return config.Load(env.Paths.ConfigPath)
}

func existingSession(env *Env) (*session, error) {
	cfg, err := existingConfig(env)
	if err != nil {
		return nil, err
	}
	return sessionFor(env, cfg), nil
}

func (s *session) sourceIndex(name string) (int, error) {
	i := slices.IndexFunc(s.cfg.Sources, func(src config.Source) bool { return src.Name == name })
	if i < 0 {
		names := make([]string, 0, len(s.cfg.Sources))
		for _, src := range s.cfg.Sources {
			names = append(names, src.Name)
		}
		return 0, fmt.Errorf("the source %s is not in the config. Use one of these sources: %s", name, strings.Join(names, ", "))
	}
	return i, nil
}

func AddSource(ctx context.Context, env *Env) error {
	s, err := existingSession(env)
	if err != nil {
		return err
	}
	if err := s.addSource(ctx); err != nil {
		return err
	}
	if err := s.confirmAndSave(); err != nil {
		return err
	}
	name := s.src().Name
	return s.offerBackup(ctx, fmt.Sprintf("Make a backup of the source %s now?", name), name)
}

func EditSource(ctx context.Context, env *Env, name string) error {
	s, err := existingSession(env)
	if err != nil {
		return err
	}
	if s.current, err = s.sourceIndex(name); err != nil {
		return err
	}
	if err := s.configureSource(ctx); err != nil {
		return err
	}
	if err := s.confirmAndSave(); err != nil {
		return err
	}
	name = s.src().Name
	return s.offerBackup(ctx, fmt.Sprintf("Make a backup of the source %s now?", name), name)
}

func RemoveSource(env *Env, name string) error {
	s, err := existingSession(env)
	if err != nil {
		return err
	}
	i, err := s.sourceIndex(name)
	if err != nil {
		return err
	}
	if len(s.cfg.Sources) == 1 {
		return fmt.Errorf("the source %s is the only source. dbak needs one or more sources. To remove dbak, run: sudo dbak uninstall", name)
	}
	if err := s.removeSourceAt(i); err != nil {
		return err
	}
	if err := s.save(); err != nil {
		return err
	}
	s.env.UI.Success(fmt.Sprintf("dbak removed the source %s from %s. The backups on the SFTP server stay.", name, env.Paths.ConfigPath))
	return nil
}

func ConfigureNotifications(ctx context.Context, env *Env) error {
	s, err := existingSession(env)
	if err != nil {
		return err
	}
	if err := s.configureNotify(ctx); err != nil {
		return err
	}
	return s.confirmAndSave()
}
