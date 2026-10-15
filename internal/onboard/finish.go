package onboard

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/state"
)

const masked = "********"

func (s *session) summary() string {
	c := s.cfg
	lines := []string{
		"Database engine:   " + c.Source.Engine,
		fmt.Sprintf("Database server:   %s:%d (ssl_mode %s)", c.Source.Host, c.Source.Port, c.Source.SSLMode),
		"Database user:     " + c.Source.Username,
		"Database password: " + masked,
		"Databases:         " + strings.Join(c.Source.Databases, ", "),
		fmt.Sprintf("SFTP server:       %s@%s:%d", c.Destination.Username, c.Destination.Host, c.Destination.Port),
		"Remote directory:  " + path.Join(c.Destination.RemotePath, s.env.Hostname),
		"Private key:       " + c.Destination.PrivateKeyFile,
		"Schedule:          " + describeSchedule(c.Schedule),
		fmt.Sprintf("Retention:         %d daily, %d weekly, %d monthly", c.Retention.Daily, c.Retention.Weekly, c.Retention.Monthly),
		"Notifications:     " + c.Notify.When,
		"Webhook:           " + maskURL(c.Notify.WebhookURL),
		"Telegram:          " + describeTelegram(c.Notify),
		"Healthcheck:       " + maskURL(c.Notify.HealthcheckURL),
	}
	return strings.Join(lines, "\n")
}

func describeSchedule(sc config.Schedule) string {
	var text string
	switch sc.Frequency {
	case config.FrequencyEveryNHours:
		text = fmt.Sprintf("every %d hours", sc.EveryHours)
	case config.FrequencyWeekly:
		text = fmt.Sprintf("weekly on %s at %s", sc.Weekday, sc.Time)
	default:
		text = "daily at " + sc.Time
	}
	text += " " + sc.Timezone
	if sc.RandomizedDelay != "" && sc.RandomizedDelay != "0" {
		text += ", random delay up to " + sc.RandomizedDelay
	}
	return text
}

func maskURL(raw string) string {
	if raw == "" {
		return "not set"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return masked
	}
	if u.Path == "" || u.Path == "/" {
		return u.Scheme + "://" + u.Host
	}
	return u.Scheme + "://" + u.Host + "/" + masked
}

func describeTelegram(n config.Notify) string {
	if n.TelegramChatID == "" {
		return "not set"
	}
	return "chat " + n.TelegramChatID + ", token " + masked
}

func (s *session) confirmAndSave() error {
	ui := s.env.UI
	ui.Heading("Summary")
	ui.Code(s.summary())
	if err := s.cfg.Validate(); err != nil {
		return err
	}
	save, err := ui.Confirm("Save this config?", "", true)
	if err != nil {
		return err
	}
	if !save {
		return ErrAborted
	}
	if err := s.save(); err != nil {
		return err
	}
	ui.Success("dbak saved the config in " + s.env.Paths.ConfigPath + ".")
	return nil
}

func (s *session) save() error {
	if err := s.ensureSecretsDir(); err != nil {
		return err
	}
	if s.password == "" {
		return errors.New("the database password is empty. Run dbak init again and type the password")
	}
	if err := s.writeSecret(s.cfg.Source.PasswordFile, []byte(s.password+"\n")); err != nil {
		return err
	}
	if s.cfg.Notify.TelegramTokenFile != "" {
		if err := s.writeSecret(s.cfg.Notify.TelegramTokenFile, []byte(s.telegramToken+"\n")); err != nil {
			return err
		}
	}
	if err := config.Save(s.env.Paths.ConfigPath, &s.cfg); err != nil {
		return err
	}
	if err := s.env.Owner.Apply(s.env.Paths.ConfigPath, 0o600); err != nil {
		return err
	}
	return s.env.Owner.MkdirAll(s.env.Paths.StateDir, 0o750)
}

func (s *session) installSchedule(ctx context.Context) {
	ui := s.env.UI
	res, err := s.env.InstallSchedule(ctx, s.cfg.Schedule)
	for _, w := range res.Warnings {
		ui.Warn(w)
	}
	if err != nil {
		ui.Error(app.Sentence(err.Error()))
		ui.Info("The config is saved, but no schedule is installed. Correct the problem, then run: sudo dbak schedule")
		return
	}
	ui.Success(res.Summary())
}

func (s *session) offerBackup(ctx context.Context, title string) error {
	ui := s.env.UI
	run, err := ui.Confirm(title, "", true)
	if err != nil || !run {
		if err == nil {
			ui.Info("To make a backup, run: dbak run")
		}
		return err
	}
	ui.Heading("Backup")
	record, runErr := s.env.RunBackup(ctx)
	if err := s.fixStateOwner(); err != nil {
		ui.Warn(app.Sentence(err.Error()))
	}
	showRun(ui, record)
	if runErr != nil {
		ui.Error(app.Sentence(runErr.Error()))
		ui.Info("Correct the problem, then run: dbak run")
	}
	return nil
}

func showRun(ui UI, record state.Run) {
	for _, db := range record.Databases {
		if db.Status == state.StatusSuccess {
			ui.Success(fmt.Sprintf("%s: %s in %s. File: %s", db.Name, humanSize(db.SizeBytes), db.Duration.Round(time.Millisecond), db.RemotePath))
		} else {
			ui.Error(fmt.Sprintf("%s: %s", db.Name, app.Sentence(db.Error)))
		}
	}
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (s *session) fixStateOwner() error {
	return filepath.WalkDir(s.env.Paths.StateDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return s.env.Owner.Apply(p, info.Mode().Perm())
	})
}

func loadSecretIfPresent(file string) string {
	if file == "" {
		return ""
	}
	if _, err := os.Stat(file); err != nil {
		return ""
	}
	secret, err := config.ReadSecret(file)
	if err != nil {
		return ""
	}
	return secret
}
