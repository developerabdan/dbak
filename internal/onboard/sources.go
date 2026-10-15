package onboard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/developerabdan/dbak/internal/config"
)

const (
	actionAdd    = "add"
	actionRemove = "remove"
)

func (s *session) configureSources(ctx context.Context) error {
	for {
		if err := s.addSource(ctx); err != nil {
			return err
		}
		more, err := s.env.UI.Confirm("Add another database source?",
			"Add a source if dbak must make backups from more than one database server.", false)
		if err != nil || !more {
			return err
		}
	}
}

func (s *session) addSource(ctx context.Context) error {
	s.cfg.Sources = append(s.cfg.Sources, config.Source{
		Host:     "127.0.0.1",
		Username: "backup_ro",
	})
	s.current = len(s.cfg.Sources) - 1
	return s.configureSource(ctx)
}

func (s *session) manageSources(ctx context.Context) error {
	ui := s.env.UI
	opts := make([]Option, 0, len(s.cfg.Sources)+2)
	for i, source := range s.cfg.Sources {
		label := fmt.Sprintf("Change %s (%s at %s:%d)", source.Name, source.Engine, source.Host, source.Port)
		opts = append(opts, Option{Label: label, Value: strconv.Itoa(i)})
	}
	opts = append(opts, Option{Label: "Add a database source", Value: actionAdd})
	if len(s.cfg.Sources) > 1 {
		opts = append(opts, Option{Label: "Remove a database source", Value: actionRemove})
	}
	choice, err := ui.Select("Select the database source to change.", "", opts, opts[0].Value)
	if err != nil {
		return err
	}
	switch choice {
	case actionAdd:
		return s.addSource(ctx)
	case actionRemove:
		return s.removeSource()
	}
	s.current, _ = strconv.Atoi(choice)
	return s.configureSource(ctx)
}

func (s *session) removeSource() error {
	ui := s.env.UI
	opts := make([]Option, 0, len(s.cfg.Sources))
	for i, source := range s.cfg.Sources {
		opts = append(opts, Option{Label: source.Name, Value: strconv.Itoa(i)})
	}
	choice, err := ui.Select("Select the database source to remove.", "", opts, opts[0].Value)
	if err != nil {
		return err
	}
	i, _ := strconv.Atoi(choice)
	return s.removeSourceAt(i)
}

func (s *session) removeSourceAt(i int) error {
	removed := s.cfg.Sources[i]
	ok, err := s.env.UI.Confirm(fmt.Sprintf("Remove the source %s?", removed.Name),
		"dbak will not make backups of its databases. The backups on the SFTP server stay.", false)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAborted
	}
	s.cfg.Sources = slices.Delete(s.cfg.Sources, i, i+1)
	delete(s.passwords, removed.Name)
	s.removed = append(s.removed, removed.PasswordFile)
	s.current = 0
	return nil
}

func (s *session) askSourceName() error {
	ui := s.env.UI
	src := s.src()
	old := src.Name
	value := old
	if value == "" {
		value = s.uniqueName(src.Engine)
	}
	name, err := ui.Input("Source name",
		"dbak uses the name in the remote directory and in the notifications. Use only letters, digits, and underscores.",
		value, s.validSourceName)
	if err != nil {
		return err
	}
	if name == old {
		return nil
	}
	if old != "" {
		ui.Warn(fmt.Sprintf("The backups of %s on the SFTP server stay in the old directory. The retention will not remove them.", old))
		s.passwords[name] = s.passwords[old]
		delete(s.passwords, old)
		s.removed = append(s.removed, src.PasswordFile)
	}
	src.Name = name
	src.PasswordFile = s.env.Paths.SourcePasswordFile(name)
	return nil
}

func (s *session) validSourceName(name string) error {
	if !config.ValidName(name) {
		return errors.New("use only letters, digits, and underscores")
	}
	for i, source := range s.cfg.Sources {
		if i != s.current && source.Name == name {
			return errors.New("a different source has this name. Type a different name")
		}
	}
	return nil
}

func (s *session) uniqueName(base string) string {
	name := base
	for n := 2; ; n++ {
		if _, used := s.cfg.SourceByName(name); !used {
			return name
		}
		name = fmt.Sprintf("%s_%d", base, n)
	}
}
