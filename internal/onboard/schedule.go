package onboard

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/schedule"
)

var weekdays = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

var everyHours = []string{"1", "2", "3", "4", "6", "8", "12"}

func (s *session) configureSchedule() error {
	ui := s.env.UI
	ui.Heading("Schedule")
	sc := &s.cfg.Schedule
	var err error
	sc.Frequency, err = ui.Select("How often must dbak make a backup?", "",
		options("Daily", config.FrequencyDaily, "Every N hours", config.FrequencyEveryNHours, "Weekly", config.FrequencyWeekly), sc.Frequency)
	if err != nil {
		return err
	}
	switch sc.Frequency {
	case config.FrequencyEveryNHours:
		current := strconv.Itoa(sc.EveryHours)
		if sc.EveryHours == 0 {
			current = "6"
		}
		hours, err := ui.Select("Interval in hours", "", sameOptions(everyHours...), current)
		if err != nil {
			return err
		}
		sc.EveryHours, _ = strconv.Atoi(hours)
		sc.Time, sc.Weekday = "", ""
	case config.FrequencyWeekly:
		if sc.Weekday == "" {
			sc.Weekday = "Sun"
		}
		if sc.Weekday, err = ui.Select("Day of the week", "", sameOptions(weekdays...), sc.Weekday); err != nil {
			return err
		}
		fallthrough
	default:
		if sc.Time == "" {
			sc.Time = "02:00"
		}
		if sc.Time, err = ui.Input("Time of the backup", "Use the HH:MM format, for example 02:00.", sc.Time, validClock); err != nil {
			return err
		}
		sc.EveryHours = 0
		if sc.Frequency == config.FrequencyDaily {
			sc.Weekday = ""
		}
	}
	if sc.Timezone == "" {
		sc.Timezone = s.env.Timezone
	}
	if sc.Timezone, err = ui.Input("Timezone", "The timezone of this server is "+s.env.Timezone+".", sc.Timezone, validZone); err != nil {
		return err
	}
	if sc.RandomizedDelay == "" {
		sc.RandomizedDelay = "0"
	}
	if sc.RandomizedDelay, err = ui.Select("Random delay", "Use a random delay if many servers send backups to the same SFTP server.",
		options("No delay", "0", "Up to 5 minutes", "5m", "Up to 15 minutes", "15m"), sc.RandomizedDelay); err != nil {
		return err
	}
	s.timezoneWarning()
	return nil
}

func (s *session) timezoneWarning() {
	sc := s.cfg.Schedule
	if sc.Frequency == config.FrequencyEveryNHours {
		return
	}
	server := s.env.Timezone
	if sc.Timezone == server && !isUTC(server) {
		return
	}
	if sc.Timezone == server || (isUTC(sc.Timezone) && isUTC(server)) {
		s.env.UI.Warn(fmt.Sprintf("This server uses UTC. The backup starts at %s UTC. Make sure that this is the correct time.", sc.Time))
		return
	}
	example, err := schedule.ConvertClock(sc.Time, sc.Timezone, server, s.env.now())
	if err != nil {
		return
	}
	s.env.UI.Warn(fmt.Sprintf("The timezone of this server is %s. The schedule uses %s: %s.", server, sc.Timezone, example))
}

func (s *session) configureRetention() error {
	ui := s.env.UI
	ui.Heading("Retention")
	ui.Info("dbak keeps the newest backup of each day, week, and month. It never removes the newest backup.")
	r := &s.cfg.Retention
	for {
		var err error
		if r.Daily, err = askCount(ui, "Daily backups to keep", r.Daily); err != nil {
			return err
		}
		if r.Weekly, err = askCount(ui, "Weekly backups to keep", r.Weekly); err != nil {
			return err
		}
		if r.Monthly, err = askCount(ui, "Monthly backups to keep", r.Monthly); err != nil {
			return err
		}
		if r.Daily+r.Weekly+r.Monthly > 0 {
			return nil
		}
		ui.Error("The total is zero. Keep one or more backups.")
	}
}

func askCount(ui UI, title string, value int) (int, error) {
	text, err := ui.Input(title, "", strconv.Itoa(value), func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return errors.New("type zero or a larger number")
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(text)
	return n, nil
}

func (s *session) configureNotify(ctx context.Context) error {
	ui := s.env.UI
	ui.Heading("Notifications")
	n := &s.cfg.Notify
	var err error
	if n.When, err = ui.Select("When must dbak send a notification?", "",
		options("Only if a backup fails", config.NotifyFailure, "After each backup", config.NotifyAlways, "Never", config.NotifyNever), n.When); err != nil {
		return err
	}
	if n.When != config.NotifyNever {
		if n.WebhookURL, err = ui.Input("Webhook URL", "dbak sends a POST request with JSON. Leave the field empty if you do not use a webhook.", n.WebhookURL, optionalURL); err != nil {
			return err
		}
		if err := s.configureTelegram(ctx); err != nil {
			return err
		}
	}
	n.HealthcheckURL, err = ui.Input("Healthcheck URL", "dbak sends a request to <URL>/start when a run starts, to <URL> after a good run, and to <URL>/fail after a failure. Leave the field empty if you do not use it.", n.HealthcheckURL, optionalURL)
	return err
}

func (s *session) configureTelegram(ctx context.Context) error {
	ui := s.env.UI
	n := &s.cfg.Notify
	use, err := ui.Confirm("Send notifications to Telegram?", "", n.TelegramChatID != "")
	if err != nil {
		return err
	}
	for use {
		if err := s.askTelegram(); err != nil {
			return err
		}
		ui.Info("dbak sends a test message to the Telegram chat.")
		err := s.env.SendTelegramTest(ctx, s.telegramToken, n.TelegramChatID)
		if err == nil {
			ui.Success("dbak sent the test message. Make sure that it is in the Telegram chat.")
			break
		}
		ui.Error("The test message failed: " + app.Sentence(err.Error()))
		ui.Info("Make sure that the bot token is correct and that the bot is a member of the chat.")
		choice, err := ui.Select("What do you want to do?", "",
			options("Type the token and the chat ID again", "retry", "Keep the values without a good test", "keep", "Do not use Telegram", "skip"), "retry")
		if err != nil {
			return err
		}
		switch choice {
		case "keep":
			ui.Warn("Telegram notifications can fail.")
			return nil
		case "skip":
			use = false
		}
	}
	if !use {
		n.TelegramChatID, n.TelegramTokenFile, s.telegramToken = "", "", ""
	}
	return nil
}

func (s *session) askTelegram() error {
	ui := s.env.UI
	n := &s.cfg.Notify
	description := "dbak keeps the token in a file with mode 0600."
	validate := required
	if s.telegramToken != "" {
		description = "Leave the field empty to keep the current token."
		validate = nil
	}
	token, err := ui.Secret("Telegram bot token", description, validate)
	if err != nil {
		return err
	}
	if token != "" {
		s.telegramToken = token
	}
	if n.TelegramChatID, err = ui.Input("Telegram chat ID", "", n.TelegramChatID, required); err != nil {
		return err
	}
	n.TelegramTokenFile = s.env.Paths.Secret(telegramFileName)
	return nil
}

func validClock(value string) error {
	if _, _, err := config.ParseClock(value); err != nil {
		return errors.New("use the HH:MM format, for example 02:00")
	}
	return nil
}

func validZone(value string) error {
	if _, err := time.LoadLocation(value); err != nil || value == "" || value == "Local" {
		return errors.New("type a timezone name, for example Asia/Jakarta")
	}
	return nil
}

func optionalURL(value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("type a URL that starts with http:// or https://")
	}
	return nil
}

func isUTC(zone string) bool {
	switch zone {
	case "UTC", "Etc/UTC", "Etc/Universal", "Universal", "Etc/Zulu", "Zulu", "Etc/GMT", "GMT":
		return true
	}
	return false
}
