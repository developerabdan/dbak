package config

const CurrentVersion = 1

const (
	EnginePostgres = "postgres"
	EngineMySQL    = "mysql"
)

const (
	FrequencyDaily       = "daily"
	FrequencyEveryNHours = "every_n_hours"
	FrequencyWeekly      = "weekly"
)

const (
	NotifyFailure = "failure"
	NotifyAlways  = "always"
	NotifyNever   = "never"
)

type Config struct {
	Version     int         `yaml:"version"`
	Source      Source      `yaml:"source"`
	Destination Destination `yaml:"destination"`
	Schedule    Schedule    `yaml:"schedule"`
	Retention   Retention   `yaml:"retention"`
	Notify      Notify      `yaml:"notify"`
}

type Source struct {
	Engine       string   `yaml:"engine"`
	Host         string   `yaml:"host"`
	Port         int      `yaml:"port"`
	Username     string   `yaml:"username"`
	PasswordFile string   `yaml:"password_file"`
	SSLMode      string   `yaml:"ssl_mode"`
	Databases    []string `yaml:"databases"`
}

type Destination struct {
	Type           string `yaml:"type"`
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	Username       string `yaml:"username"`
	PrivateKeyFile string `yaml:"private_key_file"`
	KnownHostsFile string `yaml:"known_hosts_file"`
	RemotePath     string `yaml:"remote_path"`
}

type Schedule struct {
	Frequency       string `yaml:"frequency"`
	Time            string `yaml:"time"`
	EveryHours      int    `yaml:"every_hours"`
	Weekday         string `yaml:"weekday"`
	Timezone        string `yaml:"timezone"`
	RandomizedDelay string `yaml:"randomized_delay"`
}

type Retention struct {
	Daily   int `yaml:"daily"`
	Weekly  int `yaml:"weekly"`
	Monthly int `yaml:"monthly"`
}

type Notify struct {
	When              string `yaml:"when"`
	WebhookURL        string `yaml:"webhook_url"`
	TelegramTokenFile string `yaml:"telegram_token_file"`
	TelegramChatID    string `yaml:"telegram_chat_id"`
	HealthcheckURL    string `yaml:"healthcheck_url"`
}

func DefaultPort(engine string) int {
	switch engine {
	case EnginePostgres:
		return 5432
	case EngineMySQL:
		return 3306
	}
	return 0
}

func DefaultSSLMode(engine string) string {
	switch engine {
	case EnginePostgres:
		return "prefer"
	case EngineMySQL:
		return "preferred"
	}
	return ""
}

func (c *Config) applyDefaults() {
	if c.Source.Port == 0 {
		c.Source.Port = DefaultPort(c.Source.Engine)
	}
	if c.Source.SSLMode == "" {
		c.Source.SSLMode = DefaultSSLMode(c.Source.Engine)
	}
	if c.Destination.Port == 0 {
		c.Destination.Port = 22
	}
	if c.Notify.When == "" {
		c.Notify.When = NotifyFailure
	}
}
