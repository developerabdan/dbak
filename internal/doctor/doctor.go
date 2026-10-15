package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/backup"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/encrypt"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/engine/postgres"
	"github.com/developerabdan/dbak/internal/schedule"
	"github.com/developerabdan/dbak/internal/sftp"
	"github.com/developerabdan/dbak/internal/system"
)

type Level string

const (
	OK   Level = "OK"
	Warn Level = "WARN"
	Fail Level = "FAIL"
)

type Result struct {
	Name    string
	Level   Level
	Message string
	Fix     string
}

type SFTPClient interface {
	WriteCheck(dir string) error
	Close() error
}

type Env struct {
	ConfigPath  string
	StateDir    string
	Hostname    string
	Owner       *system.Owner
	NewEngine   func(source config.Source, password string) (engine.Full, error)
	DialSFTP    func(cfg sftp.Config) (SFTPClient, error)
	Schedule    func(ctx context.Context) (schedule.Installed, error)
	CronRunning func() bool
	Run         schedule.Runner
}

type doctor struct {
	env     *Env
	cfg     *config.Config
	results []Result
}

func Run(ctx context.Context, env *Env) []Result {
	d := &doctor{env: env}
	if !d.checkConfig() {
		return d.results
	}
	d.checkPermissions()
	for _, source := range d.cfg.Sources {
		if eng, ok := d.checkDatabase(ctx, source); ok {
			d.checkDumps(ctx, source, eng)
		}
	}
	d.checkSFTP()
	d.checkEncryption()
	d.checkSchedule(ctx)
	d.checkClock(ctx)
	return d.results
}

func Failed(results []Result) int {
	n := 0
	for _, r := range results {
		if r.Level == Fail {
			n++
		}
	}
	return n
}

func (d *doctor) add(name string, level Level, message, fix string) {
	d.results = append(d.results, Result{Name: name, Level: level, Message: message, Fix: fix})
}

func (d *doctor) checkConfig() bool {
	cfg, err := config.Load(d.env.ConfigPath)
	if err == nil {
		err = cfg.Validate()
	}
	if err != nil {
		d.add("Config", Fail, app.Sentence(err.Error()), "Correct the config, or run: sudo dbak init")
		return false
	}
	d.cfg = cfg
	d.add("Config", OK, d.env.ConfigPath+" is valid.", "")
	return true
}

type fileRule struct {
	path string
	mode os.FileMode
	dir  bool
}

func (d *doctor) fileRules() []fileRule {
	secretsDir := filepath.Join(filepath.Dir(d.env.ConfigPath), "secrets")
	rules := []fileRule{
		{path: d.env.ConfigPath, mode: 0o600},
		{path: secretsDir, mode: 0o700, dir: true},
	}
	for _, source := range d.cfg.Sources {
		if dir := filepath.Dir(source.PasswordFile); dir != secretsDir {
			rules = append(rules, fileRule{path: dir, mode: 0o700, dir: true})
		}
		rules = append(rules, fileRule{path: source.PasswordFile, mode: 0o600})
	}
	rules = append(rules,
		fileRule{path: d.cfg.Destination.PrivateKeyFile, mode: 0o600},
		fileRule{path: d.cfg.Destination.KnownHostsFile, mode: 0o600},
	)
	if d.cfg.Notify.TelegramTokenFile != "" {
		rules = append(rules, fileRule{path: d.cfg.Notify.TelegramTokenFile, mode: 0o600})
	}
	return append(rules, fileRule{path: d.env.StateDir, mode: 0o750, dir: true})
}

func (d *doctor) checkPermissions() {
	if d.env.Owner == nil {
		d.add("Permissions", Fail, fmt.Sprintf("The user %s does not exist.", system.ServiceUser), "Run: sudo dbak init")
		return
	}
	problems := 0
	for _, rule := range d.fileRules() {
		info, err := os.Stat(rule.path)
		if errors.Is(err, os.ErrNotExist) {
			d.add("Permissions", Fail, rule.path+" does not exist.", "Run: sudo dbak init")
			problems++
			continue
		}
		if err != nil {
			d.add("Permissions", Fail, fmt.Sprintf("Cannot examine %s: %v.", rule.path, err), "Run the command with sudo.")
			problems++
			continue
		}
		if uid, ok := fileUID(info); ok && uid != d.env.Owner.UID {
			d.add("Permissions", Fail, fmt.Sprintf("The owner of %s is not %s.", rule.path, system.ServiceUser),
				fmt.Sprintf("Run: sudo chown %s:%s %s", system.ServiceUser, system.ServiceUser, rule.path))
			problems++
		}
		if extra := info.Mode().Perm() &^ rule.mode; extra != 0 {
			d.add("Permissions", Fail, fmt.Sprintf("%s has mode %04o. The mode must be %04o or less.", rule.path, info.Mode().Perm(), rule.mode),
				fmt.Sprintf("Run: sudo chmod %04o %s", rule.mode, rule.path))
			problems++
		}
	}
	if problems == 0 {
		d.add("Permissions", OK, "The config, the secrets, and the state directory have the correct owner and mode.", "")
	}
}

func fileUID(info os.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

func (d *doctor) checkDatabase(ctx context.Context, source config.Source) (engine.Full, bool) {
	database, tool := "Database "+source.Name, "Client tool "+source.Name
	password, err := config.ReadSecret(source.PasswordFile)
	if err != nil {
		d.add(database, Fail, app.Sentence(err.Error()), "Run the command with sudo, or run: sudo dbak init")
		return nil, false
	}
	eng, err := d.env.NewEngine(source, password)
	if err != nil {
		d.add(database, Fail, app.Sentence(err.Error()), "Correct the engine of the source "+source.Name+" in the config.")
		return nil, false
	}
	client, err := eng.ClientVersion(ctx)
	if err != nil {
		d.add(tool, Fail, app.Sentence(err.Error()), "Install the client tool, or correct bin_dir of the source "+source.Name+".")
		return nil, false
	}
	info, err := eng.ProbeServer(ctx)
	if err != nil {
		d.add(tool, OK, client.Text, "")
		d.add(database, Fail, app.Sentence(err.Error()), "Make sure that the database server runs and accepts the backup user.")
		return nil, false
	}
	d.checkClient(tool, source, client, info)
	d.add(database, OK, fmt.Sprintf("Connected to %s %s at %s:%d.", flavorName(info.Flavor), info.Version, source.Host, source.Port), "")
	return eng, true
}

func (d *doctor) checkClient(name string, source config.Source, client engine.ClientInfo, info engine.ServerInfo) {
	if source.Engine == config.EnginePostgres {
		var tooOld *engine.ClientTooOldError
		if err := postgres.CheckClient(client, info); errors.As(err, &tooOld) {
			d.add(name, Fail, app.Sentence(err.Error()), fmt.Sprintf("Install the PostgreSQL %d client tools from the PGDG repository, then set bin_dir of the source %s.", tooOld.Server, source.Name))
			return
		}
	}
	if info.Flavor == engine.FlavorMySQL && client.MariaDB {
		d.add(name, Warn, client.Text+". The server is MySQL, but the client tool is from MariaDB.", "Install the MySQL client tools if the backup fails.")
		return
	}
	d.add(name, OK, client.Text, "")
}

func flavorName(flavor string) string {
	switch flavor {
	case engine.FlavorMariaDB:
		return "MariaDB"
	case engine.FlavorMySQL:
		return "MySQL"
	}
	return "PostgreSQL"
}

func (d *doctor) checkDumps(ctx context.Context, source config.Source, eng engine.Full) {
	for _, db := range source.Databases {
		name := "Dump check " + source.Name + "/" + db
		report, err := eng.PrivilegeCheck(ctx, db)
		if err != nil {
			d.add(name, Fail, "Cannot examine the privileges: "+app.Sentence(err.Error()), "Make sure that the backup user can connect to "+db+".")
			continue
		}
		if !report.OK() {
			d.add(name, Fail, "The backup user cannot read: "+strings.Join(report.Problems, ", ")+".",
				"Run this SQL as a superuser in "+db+": "+strings.Join(strings.Fields(report.FixSQL), " "))
			continue
		}
		if err := eng.SchemaCheck(ctx, db); err != nil {
			d.add(name, Fail, "The schema dump failed: "+app.Sentence(err.Error()), "Correct the privileges of the backup user.")
			continue
		}
		d.add(name, OK, "The backup user can read all objects.", "")
	}
}

func (d *doctor) checkSFTP() {
	client, err := d.env.DialSFTP(backup.SFTPConfig(d.cfg.Destination))
	if err != nil {
		fix := "Make sure that the SFTP server runs and accepts the key."
		if errors.Is(err, sftp.ErrHostKeyChanged) {
			fix = "Find the cause of the change. If the change is correct, run: sudo dbak init"
		}
		d.add("SFTP", Fail, app.Sentence(err.Error()), fix)
		return
	}
	defer client.Close()
	dir := path.Join(d.cfg.Destination.RemotePath, d.env.Hostname)
	if err := client.WriteCheck(dir); err != nil {
		d.add("SFTP", Fail, app.Sentence(err.Error()), "Make sure that the SFTP user has write permission in "+d.cfg.Destination.RemotePath+".")
		return
	}
	d.add("SFTP", OK, fmt.Sprintf("dbak can write to %s on %s.", dir, d.cfg.Destination.Host), "")
}

func (d *doctor) checkEncryption() {
	recipients := d.cfg.Encryption.Recipients
	if len(recipients) == 0 {
		d.add("Encryption", Warn, "The backups are not encrypted. A person with access to the SFTP server can read them.",
			"Run: sudo dbak init, then select Backup encryption.")
		return
	}
	if _, err := encrypt.ParseRecipients(recipients); err != nil {
		d.add("Encryption", Fail, app.Sentence(err.Error()), "Correct encryption.recipients in the config.")
		return
	}
	noun := "public keys"
	if len(recipients) == 1 {
		noun = "public key"
	}
	d.add("Encryption", OK, fmt.Sprintf("The backups are encrypted for %d %s.", len(recipients), noun), "")
}

func (d *doctor) checkSchedule(ctx context.Context) {
	installed, err := d.env.Schedule(ctx)
	if err != nil {
		d.add("Schedule", Fail, app.Sentence(err.Error()), "Run: sudo dbak schedule")
		return
	}
	switch installed.Mode {
	case schedule.ModeSystemd:
		if installed.Enabled != "enabled" || installed.Active != "active" {
			d.add("Schedule", Fail, fmt.Sprintf("The systemd timer %s is %s and %s.", schedule.TimerName, installed.Enabled, installed.Active), "Run: sudo dbak schedule")
			return
		}
		d.add("Schedule", OK, fmt.Sprintf("The systemd timer %s is active. OnCalendar=%s", schedule.TimerName, installed.Expression), "")
	case schedule.ModeCron:
		if d.env.CronRunning != nil && !d.env.CronRunning() {
			d.add("Schedule", Warn, "The cron file is installed, but dbak cannot find a cron process.", "Make sure that the cron service runs.")
			return
		}
		d.add("Schedule", OK, "The cron file is installed. Time: "+installed.Expression, "")
	default:
		d.add("Schedule", Fail, "No schedule is installed.", "Run: sudo dbak schedule")
	}
}

func (d *doctor) checkClock(ctx context.Context) {
	out, err := d.env.Run(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value")
	switch {
	case err != nil:
		d.add("Clock", Warn, "dbak cannot examine the clock synchronization.", "Make sure that an NTP service runs on this server.")
	case strings.TrimSpace(out) == "yes":
		d.add("Clock", OK, "The system clock is synchronized with NTP.", "")
	default:
		d.add("Clock", Warn, "The system clock is not synchronized with NTP.", "Run: sudo timedatectl set-ntp true")
	}
}
