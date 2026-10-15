package config

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

var postgresSSLModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

var mysqlSSLModes = []string{"disabled", "preferred", "required", "verify_ca", "verify_identity"}

var everyHoursChoices = []int{1, 2, 3, 4, 6, 8, 12}

var weekdays = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

func ValidName(name string) bool {
	return namePattern.MatchString(name)
}

type Problem struct {
	Field   string
	Message string
}

type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	lines := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		lines = append(lines, fmt.Sprintf("%s: %s", p.Field, p.Message))
	}
	return "the config is not valid:\n  " + strings.Join(lines, "\n  ")
}

type validator struct {
	problems []Problem
}

func (v *validator) add(field, format string, args ...any) {
	v.problems = append(v.problems, Problem{Field: field, Message: fmt.Sprintf(format, args...)})
}

func (c *Config) Validate() error {
	v := &validator{}
	if c.Version != CurrentVersion {
		v.add("version", "The version %d is not known. Use version %d.", c.Version, CurrentVersion)
	}
	v.source(c.Source)
	v.destination(c.Destination)
	v.schedule(c.Schedule)
	v.retention(c.Retention)
	v.notify(c.Notify)
	if len(v.problems) == 0 {
		return nil
	}
	return &ValidationError{Problems: v.problems}
}

func (v *validator) source(s Source) {
	switch s.Engine {
	case EnginePostgres:
		if !slices.Contains(postgresSSLModes, s.SSLMode) {
			v.add("source.ssl_mode", "The value %q is not known. Use one of: %s.", s.SSLMode, strings.Join(postgresSSLModes, ", "))
		}
	case EngineMySQL:
		if !slices.Contains(mysqlSSLModes, strings.ToLower(s.SSLMode)) {
			v.add("source.ssl_mode", "The value %q is not known. Use one of: %s.", s.SSLMode, strings.Join(mysqlSSLModes, ", "))
		}
	default:
		v.add("source.engine", "The engine %q is not known. Use postgres or mysql.", s.Engine)
	}
	v.required("source.host", s.Host)
	v.port("source.port", s.Port)
	v.required("source.username", s.Username)
	v.absolutePath("source.password_file", s.PasswordFile)
	if len(s.Databases) == 0 {
		v.add("source.databases", "The list is empty. Add one or more database names.")
	}
	seen := map[string]bool{}
	for i, db := range s.Databases {
		field := fmt.Sprintf("source.databases[%d]", i)
		if !ValidName(db) {
			v.add(field, "The name %q is not valid. Use only letters, digits, and underscores.", db)
		}
		if seen[db] {
			v.add(field, "The database %q is in the list two times. Remove one.", db)
		}
		seen[db] = true
	}
}

func (v *validator) destination(d Destination) {
	if d.Type != "sftp" {
		v.add("destination.type", "The type %q is not known. Use sftp.", d.Type)
	}
	v.required("destination.host", d.Host)
	v.port("destination.port", d.Port)
	v.required("destination.username", d.Username)
	v.absolutePath("destination.private_key_file", d.PrivateKeyFile)
	v.absolutePath("destination.known_hosts_file", d.KnownHostsFile)
	if !path.IsAbs(d.RemotePath) {
		v.add("destination.remote_path", "The value %q is not an absolute path. Use a path that starts with /.", d.RemotePath)
	}
}

func (v *validator) schedule(s Schedule) {
	switch s.Frequency {
	case FrequencyDaily:
		v.clockTime("schedule.time", s.Time)
	case FrequencyWeekly:
		v.clockTime("schedule.time", s.Time)
		if !slices.Contains(weekdays, s.Weekday) {
			v.add("schedule.weekday", "The value %q is not known. Use one of: %s.", s.Weekday, strings.Join(weekdays, ", "))
		}
	case FrequencyEveryNHours:
		if !slices.Contains(everyHoursChoices, s.EveryHours) {
			v.add("schedule.every_hours", "The value %d is not supported. Use 1, 2, 3, 4, 6, 8, or 12.", s.EveryHours)
		}
	default:
		v.add("schedule.frequency", "The value %q is not known. Use daily, every_n_hours, or weekly.", s.Frequency)
	}
	if s.Timezone == "" {
		v.add("schedule.timezone", "The value is empty. Use a timezone name such as Asia/Jakarta.")
	} else if _, err := time.LoadLocation(s.Timezone); err != nil {
		v.add("schedule.timezone", "The timezone %q is not known. Use a name from the IANA timezone database.", s.Timezone)
	}
	if _, err := ParseDelay(s.RandomizedDelay); err != nil {
		v.add("schedule.randomized_delay", "The value %q is not valid. Use 0, 5m, or 15m.", s.RandomizedDelay)
	}
}

func ParseDelay(value string) (time.Duration, error) {
	if value == "" || value == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("the delay %s is less than zero", value)
	}
	return d, nil
}

func ParseClock(value string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", value)
	if err != nil || len(value) != 5 {
		return 0, 0, fmt.Errorf("the time %q is not in HH:MM format", value)
	}
	return t.Hour(), t.Minute(), nil
}

func (v *validator) retention(r Retention) {
	if r.Daily < 0 {
		v.add("retention.daily", "The value %d is less than zero. Use zero or more.", r.Daily)
	}
	if r.Weekly < 0 {
		v.add("retention.weekly", "The value %d is less than zero. Use zero or more.", r.Weekly)
	}
	if r.Monthly < 0 {
		v.add("retention.monthly", "The value %d is less than zero. Use zero or more.", r.Monthly)
	}
	if r.Daily+r.Weekly+r.Monthly <= 0 {
		v.add("retention", "The total of daily, weekly, and monthly is zero. Keep one or more backups.")
	}
}

func (v *validator) notify(n Notify) {
	if !slices.Contains([]string{NotifyFailure, NotifyAlways, NotifyNever}, n.When) {
		v.add("notify.when", "The value %q is not known. Use failure, always, or never.", n.When)
	}
	v.optionalURL("notify.webhook_url", n.WebhookURL)
	v.optionalURL("notify.healthcheck_url", n.HealthcheckURL)
	if (n.TelegramTokenFile == "") != (n.TelegramChatID == "") {
		v.add("notify.telegram_chat_id", "Telegram needs both telegram_token_file and telegram_chat_id. Set both or set neither.")
	}
	if n.TelegramTokenFile != "" {
		v.absolutePath("notify.telegram_token_file", n.TelegramTokenFile)
	}
}

func (v *validator) required(field, value string) {
	if strings.TrimSpace(value) == "" {
		v.add(field, "The value is empty. Set a value.")
	}
}

func (v *validator) port(field string, value int) {
	if value < 1 || value > 65535 {
		v.add(field, "The port %d is not valid. Use a number from 1 to 65535.", value)
	}
}

func (v *validator) absolutePath(field, value string) {
	if !path.IsAbs(value) {
		v.add(field, "The value %q is not an absolute path. Use a path that starts with /.", value)
	}
}

func (v *validator) clockTime(field, value string) {
	if _, _, err := ParseClock(value); err != nil {
		v.add(field, "The value %q is not a valid time. Use the HH:MM format, for example 02:00.", value)
	}
}

func (v *validator) optionalURL(field, value string) {
	if value == "" {
		return
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		v.add(field, "The value is not a valid URL. Use a URL that starts with http:// or https://.")
	}
}
