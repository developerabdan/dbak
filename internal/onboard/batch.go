package onboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/backup"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/system"
)

type Secrets struct {
	DBPassword    string
	DBPasswords   map[string]string
	TelegramToken string
}

func PasswordVariable(source string) string {
	return "DBAK_DB_PASSWORD_" + strings.ToUpper(source)
}

func (s Secrets) passwordFor(source string, sources int) string {
	if password := s.DBPasswords[strings.ToUpper(source)]; password != "" {
		return password
	}
	if sources == 1 {
		return s.DBPassword
	}
	return ""
}

type batchInput struct {
	privateKey []byte
	knownHosts []byte
}

func RunFromFile(ctx context.Context, env *Env, inputPath string, secrets Secrets) error {
	ui := env.UI
	ui.Heading("dbak setup from " + inputPath)
	cfg, err := config.Read(inputPath)
	if err != nil {
		return app.Exit(app.ExitConfigInvalid, err)
	}
	defaultSecretPaths(cfg, env.Paths)
	if err := cfg.Validate(); err != nil {
		return app.Exit(app.ExitConfigInvalid, err)
	}
	s := newSession(env)
	s.cfg = *cfg
	input, err := s.readBatchSecrets(secrets)
	if err != nil {
		return app.Exit(app.ExitConfigInvalid, err)
	}
	for i := range s.cfg.Sources {
		s.current = i
		if err := s.batchSource(ctx); err != nil {
			return err
		}
	}
	if err := s.batchDestination(); err != nil {
		return app.Exit(app.ExitUploadFailed, err)
	}
	if err := s.batchSave(input); err != nil {
		return app.Exit(app.ExitGeneral, err)
	}
	ui.Success("dbak saved the config in " + env.Paths.ConfigPath + ".")
	ui.Code(s.summary())
	res, err := env.InstallSchedule(ctx, s.cfg.Schedule)
	for _, w := range res.Warnings {
		ui.Warn(w)
	}
	if err != nil {
		return app.Exit(app.ExitGeneral, fmt.Errorf("the config is saved, but the schedule install failed: %w. Correct the problem, then run: sudo dbak schedule", err))
	}
	ui.Success(res.Summary())
	return nil
}

func defaultSecretPaths(cfg *config.Config, paths Paths) {
	for i := range cfg.Sources {
		if cfg.Sources[i].PasswordFile == "" {
			cfg.Sources[i].PasswordFile = paths.SourcePasswordFile(cfg.Sources[i].Name)
		}
	}
	if cfg.Notify.TelegramChatID != "" && cfg.Notify.TelegramTokenFile == "" {
		cfg.Notify.TelegramTokenFile = paths.Secret(telegramFileName)
	}
}

func (s *session) readBatchSecrets(secrets Secrets) (batchInput, error) {
	var input batchInput
	c := &s.cfg
	for _, src := range c.Sources {
		password := secrets.passwordFor(src.Name, len(c.Sources))
		if password == "" {
			password = loadSecretIfPresent(src.PasswordFile)
		}
		if password == "" {
			return input, fmt.Errorf("the password of the source %s is not available. Set %s, or put the password in %s", src.Name, PasswordVariable(src.Name), src.PasswordFile)
		}
		s.passwords[src.Name] = password
	}
	if c.Notify.TelegramChatID != "" {
		s.telegramToken = secrets.TelegramToken
		if s.telegramToken == "" {
			s.telegramToken = loadSecretIfPresent(c.Notify.TelegramTokenFile)
		}
		if s.telegramToken == "" {
			return input, errors.New("notify.telegram_chat_id is set, but the Telegram bot token is not available. Set DBAK_TELEGRAM_TOKEN, or set notify.telegram_token_file")
		}
	}
	if _, err := sftp.PublicKeyFromFile(c.Destination.PrivateKeyFile, ""); err != nil {
		return input, err
	}
	var err error
	if input.privateKey, err = os.ReadFile(c.Destination.PrivateKeyFile); err != nil {
		return input, fmt.Errorf("cannot read the private key file %s: %w", c.Destination.PrivateKeyFile, err)
	}
	if input.knownHosts, err = os.ReadFile(c.Destination.KnownHostsFile); err != nil {
		return input, fmt.Errorf("cannot read the known_hosts file %s. Make the file with ssh-keyscan and compare the fingerprint with the SFTP server", c.Destination.KnownHostsFile)
	}
	return input, nil
}

func (s *session) batchSource(ctx context.Context) error {
	ui := s.env.UI
	src := s.src()
	ui.Heading("Database source " + src.Name)
	tool := backup.ClientTool(*src)
	if _, err := s.env.LookPath(tool); err != nil {
		if src.BinDir != "" {
			return app.Exit(app.ExitConfigInvalid, fmt.Errorf("the client tool %s does not exist. Correct bin_dir of the source %s", tool, src.Name))
		}
		commands := system.InstallCommands(s.env.OSFamily, system.ClientPackage(s.env.OSFamily, src.Engine))
		lines := make([]string, 0, len(commands))
		for _, c := range commands {
			lines = append(lines, strings.Join(c, " "))
		}
		if len(lines) > 0 {
			ui.Info("To install the client tool, run these commands:")
			ui.Code(strings.Join(lines, "\n"))
		}
		return app.Exit(app.ExitGeneral, fmt.Errorf("the client tool %s is not installed. Install it, then run dbak init again", tool))
	}
	eng, err := s.env.NewEngine(*src, s.password())
	if err != nil {
		return app.Exit(app.ExitConfigInvalid, err)
	}
	info, err := eng.ProbeServer(ctx)
	if err != nil {
		return app.Exit(app.ExitDatabase, err)
	}
	ui.Success(fmt.Sprintf("Connected to %s %s.", flavorName(info.Flavor), info.Version))
	if err := s.checkClient(ctx, eng, info); err != nil {
		var tooOld *engine.ClientTooOldError
		if errors.As(err, &tooOld) {
			err = s.reportOldClient(tooOld, system.PGDGInstructions(s.env.OSFamily, tooOld.Server))
		}
		return app.Exit(app.ExitGeneral, err)
	}
	s.eng, s.server = eng, info
	available, err := eng.ListDatabases(ctx)
	if err != nil {
		return app.Exit(app.ExitDatabase, err)
	}
	for _, db := range src.Databases {
		if !slices.Contains(available, db) {
			return app.Exit(app.ExitConfigInvalid, fmt.Errorf("the database %s does not exist, or the backup user cannot see it. Correct the databases of the source %s", db, src.Name))
		}
	}
	ui.Heading("Dump check")
	failed := 0
	for _, db := range src.Databases {
		if !s.checkDatabase(ctx, db) {
			failed++
		}
	}
	if failed > 0 {
		return app.Exit(app.ExitGeneral, fmt.Errorf("the dump check failed for %d databases. Correct the privileges, then run dbak init again", failed))
	}
	return nil
}

func (s *session) batchDestination() error {
	ui := s.env.UI
	ui.Heading("SFTP destination")
	d := s.cfg.Destination
	client, err := s.env.DialSFTP(sftp.Config{
		Host:           d.Host,
		Port:           d.Port,
		Username:       d.Username,
		PrivateKeyFile: d.PrivateKeyFile,
		KnownHostsFile: d.KnownHostsFile,
	})
	if err != nil {
		return err
	}
	defer client.Close()
	dir := path.Join(d.RemotePath, s.env.Hostname)
	if err := client.WriteCheck(dir); err != nil {
		return err
	}
	ui.Success("The test upload to " + dir + " passed.")
	return nil
}

func (s *session) batchSave(input batchInput) error {
	if err := s.ensureSecretsDir(); err != nil {
		return err
	}
	d := &s.cfg.Destination
	d.PrivateKeyFile = s.keyFile()
	d.KnownHostsFile = s.env.Paths.Secret(knownHostsFileName)
	if err := s.writeSecret(d.PrivateKeyFile, input.privateKey); err != nil {
		return err
	}
	if err := s.writeSecret(d.KnownHostsFile, input.knownHosts); err != nil {
		return err
	}
	for i := range s.cfg.Sources {
		s.cfg.Sources[i].PasswordFile = s.env.Paths.SourcePasswordFile(s.cfg.Sources[i].Name)
	}
	if s.cfg.Notify.TelegramChatID != "" {
		s.cfg.Notify.TelegramTokenFile = s.env.Paths.Secret(telegramFileName)
	} else {
		s.cfg.Notify.TelegramTokenFile = ""
	}
	return s.save()
}
