package onboard

import (
	"context"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/schedule"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/state"
	"github.com/developerabdan/dbak/internal/system"
)

type Paths struct {
	ConfigPath string
	StateDir   string
}

func (p Paths) ConfigDir() string {
	return filepath.Dir(p.ConfigPath)
}

func (p Paths) SecretsDir() string {
	return filepath.Join(p.ConfigDir(), "secrets")
}

func (p Paths) Secret(name string) string {
	return filepath.Join(p.SecretsDir(), name)
}

func (p Paths) SourcePasswordFile(source string) string {
	return filepath.Join(p.SecretsDir(), source, passwordFileName)
}

const (
	passwordFileName   = "db_password"
	privateKeyFileName = "id_ed25519"
	knownHostsFileName = "known_hosts"
	telegramFileName   = "telegram_token"
)

type SFTPClient interface {
	WriteCheck(dir string) error
	Close() error
}

type Env struct {
	UI         UI
	Paths      Paths
	Owner      system.Owner
	Hostname   string
	Timezone   string
	PrimaryIP  string
	OSFamily   string
	HasSystemd bool
	Now        func() time.Time

	NewEngine    func(source config.Source, password string) (engine.Full, error)
	LookPath     func(file string) (string, error)
	RunCommand   func(ctx context.Context, args []string) error
	DialSFTP     func(cfg sftp.Config) (SFTPClient, error)
	FetchHostKey func(host string, port int) (ssh.PublicKey, error)
	RunBackup    func(ctx context.Context, source string) (state.Run, error)

	InstallSchedule  func(ctx context.Context, sc config.Schedule) (schedule.Result, error)
	SendTelegramTest func(ctx context.Context, token, chatID string) error
	ClearScreen      func()
}

func (e *Env) now() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}

type session struct {
	env           *Env
	cfg           config.Config
	passwords     map[string]string
	removed       []string
	current       int
	telegramToken string
	server        engine.ServerInfo
	eng           engine.Full
}

func newSession(env *Env) *session {
	return &session{env: env, cfg: defaultConfig(env), passwords: map[string]string{}}
}

func (s *session) src() *config.Source {
	return &s.cfg.Sources[s.current]
}

func (s *session) password() string {
	return s.passwords[s.src().Name]
}

func (s *session) setPassword(password string) {
	s.passwords[s.src().Name] = password
}

func (s *session) loadPasswords() {
	for _, source := range s.cfg.Sources {
		s.passwords[source.Name] = loadSecretIfPresent(source.PasswordFile)
	}
}

func defaultConfig(env *Env) config.Config {
	return config.Config{
		Version: config.CurrentVersion,
		Destination: config.Destination{
			Type:           "sftp",
			Port:           22,
			PrivateKeyFile: env.Paths.Secret(privateKeyFileName),
			KnownHostsFile: env.Paths.Secret(knownHostsFileName),
			RemotePath:     "/backups",
		},
		Schedule: config.Schedule{
			Frequency:       config.FrequencyDaily,
			Time:            "02:00",
			Weekday:         "",
			Timezone:        env.Timezone,
			RandomizedDelay: "0",
		},
		Retention: config.Retention{Daily: 7, Weekly: 4, Monthly: 6},
		Notify:    config.Notify{When: config.NotifyFailure},
	}
}
