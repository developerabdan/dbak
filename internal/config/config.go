package config

const (
	CurrentVersion = 2
	LegacyVersion  = 1
)

const LegacySourceName = "default"

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
	Sources     []Source    `yaml:"sources"`
	Destination Destination `yaml:"destination"`
	Schedule    Schedule    `yaml:"schedule"`
	Retention   Retention   `yaml:"retention"`
	Notify      Notify      `yaml:"notify"`
	Encryption  Encryption  `yaml:"encryption,omitempty"`
}

type Encryption struct {
	Recipients []string `yaml:"recipients,omitempty"`
}

func (e Encryption) Enabled() bool {
	return len(e.Recipients) > 0
}

type Source struct {
	Name         string     `yaml:"name"`
	Engine       string     `yaml:"engine"`
	Host         string     `yaml:"host"`
	Port         int        `yaml:"port"`
	Username     string     `yaml:"username"`
	PasswordFile string     `yaml:"password_file"`
	SSLMode      string     `yaml:"ssl_mode"`
	BinDir       string     `yaml:"bin_dir,omitempty"`
	Databases    []string   `yaml:"databases"`
	Retention    *Retention `yaml:"retention,omitempty"`
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
	TelegramThreadID  int    `yaml:"telegram_thread_id,omitempty"`
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

func (c *Config) SourceByName(name string) (*Source, bool) {
	for i := range c.Sources {
		if c.Sources[i].Name == name {
			return &c.Sources[i], true
		}
	}
	return nil, false
}

func (c *Config) RetentionFor(s Source) Retention {
	if s.Retention != nil {
		return *s.Retention
	}
	return c.Retention
}

func (c *Config) applyDefaults() {
	for i := range c.Sources {
		c.Sources[i].applyDefaults()
	}
	if c.Destination.Port == 0 {
		c.Destination.Port = 22
	}
	if c.Notify.When == "" {
		c.Notify.When = NotifyFailure
	}
}

func (s *Source) applyDefaults() {
	if s.Port == 0 {
		s.Port = DefaultPort(s.Engine)
	}
	if s.SSLMode == "" {
		s.SSLMode = DefaultSSLMode(s.Engine)
	}
}
