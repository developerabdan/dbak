package onboard

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/backup"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/engine/postgres"
	"github.com/developerabdan/dbak/internal/system"
)

var (
	postgresSSLModes = []string{"prefer", "disable", "allow", "require", "verify-ca", "verify-full"}
	mysqlSSLModes    = []string{"preferred", "disabled", "required", "verify_ca", "verify_identity"}
)

func (s *session) configureSource(ctx context.Context) error {
	ui := s.env.UI
	src := s.src()
	if src.Name == "" {
		ui.Heading("Database source")
	} else {
		ui.Heading("Database source " + src.Name)
	}
	current := src.Engine
	if current == "" {
		current = config.EnginePostgres
	}
	engineName, err := selectEngine(ui, current)
	if err != nil {
		return err
	}
	if engineName != src.Engine {
		src.Engine = engineName
		src.Port = config.DefaultPort(engineName)
		src.SSLMode = config.DefaultSSLMode(engineName)
	}
	if err := s.askSourceName(); err != nil {
		return err
	}
	if err := s.ensureClientTool(ctx); err != nil {
		return err
	}
	if err := s.userBranch(); err != nil {
		return err
	}
	if err := s.connect(ctx); err != nil {
		return err
	}
	if err := s.chooseDatabases(ctx); err != nil {
		return err
	}
	return s.dumpCheck(ctx)
}

func (s *session) ensureClientTool(ctx context.Context) error {
	ui := s.env.UI
	src := s.src()
	tool := backup.ClientTool(*src)
	if _, err := s.env.LookPath(tool); err == nil {
		return nil
	}
	if src.BinDir != "" {
		return fmt.Errorf("the client tool %s does not exist. Correct bin_dir of the source %s in %s", tool, src.Name, s.env.Paths.ConfigPath)
	}
	ui.Warn(fmt.Sprintf("The client tool %s is not installed.", tool))
	pkg := system.ClientPackage(s.env.OSFamily, src.Engine)
	commands := system.InstallCommands(s.env.OSFamily, pkg)
	if len(commands) == 0 {
		return fmt.Errorf("the client tool %s is not installed. Install it with the package manager of this system, then run dbak init again", tool)
	}
	lines := make([]string, 0, len(commands))
	for _, c := range commands {
		lines = append(lines, strings.Join(c, " "))
	}
	install, err := ui.Confirm(fmt.Sprintf("Install the package %s now?", pkg), "dbak will run: "+strings.Join(lines, " && "), true)
	if err != nil {
		return err
	}
	if !install {
		ui.Info("To install the client tool, run these commands:")
		ui.Code(strings.Join(lines, "\n"))
		return fmt.Errorf("the client tool %s is not installed. Install it, then run dbak init again", tool)
	}
	for _, c := range commands {
		if err := s.env.RunCommand(ctx, c); err != nil {
			return fmt.Errorf("the command %q failed: %w. Install the package manually, then run dbak init again", strings.Join(c, " "), err)
		}
	}
	if _, err := s.env.LookPath(tool); err != nil {
		return fmt.Errorf("the client tool %s is not available after the install. Install it manually, then run dbak init again", tool)
	}
	ui.Success(fmt.Sprintf("The client tool %s is installed.", tool))
	return nil
}

func (s *session) userBranch() error {
	choice, err := s.env.UI.Select("Do you have a database user for backup?", "",
		options("Yes, I will type the credentials", "yes", "No, show me the SQL to make one", "no"), "yes")
	if err != nil || choice == "yes" {
		return err
	}
	result, err := RunUserHelper(s.env.UI, s.src().Engine, s.env.PrimaryIP)
	if err != nil {
		return err
	}
	s.src().Username = result.User
	s.setPassword(result.Password)
	return nil
}

func (s *session) askCredentials() error {
	ui := s.env.UI
	src := s.src()
	var err error
	if src.Host, err = ui.Input("Database host", "Type the IP address or the host name of the database server.", src.Host, required); err != nil {
		return err
	}
	port, err := ui.Input("Database port", "", strconv.Itoa(src.Port), validPort)
	if err != nil {
		return err
	}
	src.Port, _ = strconv.Atoi(port)
	if src.Username, err = ui.Input("Database user name", "", src.Username, required); err != nil {
		return err
	}
	description := "dbak keeps the password in " + src.PasswordFile + " with mode 0600."
	validate := required
	if s.password() != "" {
		description = "Leave the field empty to keep the current password."
		validate = nil
	}
	password, err := ui.Secret("Database password", description, validate)
	if err != nil {
		return err
	}
	if password != "" {
		s.setPassword(password)
	}
	modes := postgresSSLModes
	if src.Engine == config.EngineMySQL {
		modes = mysqlSSLModes
	}
	src.SSLMode, err = ui.Select("SSL mode", "", sameOptions(modes...), strings.ToLower(src.SSLMode))
	return err
}

func (s *session) connect(ctx context.Context) error {
	ui := s.env.UI
	for {
		if err := s.askCredentials(); err != nil {
			return err
		}
		eng, err := s.env.NewEngine(*s.src(), s.password())
		if err != nil {
			return err
		}
		info, err := eng.ProbeServer(ctx)
		if err != nil {
			ui.Error(app.Sentence(err.Error()))
			ui.Info("Correct the connection settings.")
			continue
		}
		ui.Success(fmt.Sprintf("Connected to %s %s.", flavorName(info.Flavor), info.Version))
		if eng, err = s.compatibleClient(ctx, eng, info); err != nil {
			return err
		}
		s.eng = eng
		s.server = info
		return nil
	}
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

func (s *session) checkClient(ctx context.Context, eng engine.Full, info engine.ServerInfo) error {
	ui := s.env.UI
	client, err := eng.ClientVersion(ctx)
	if err != nil {
		return err
	}
	if s.src().Engine == config.EnginePostgres {
		if err := postgres.CheckClient(client, info); err != nil {
			return err
		}
	}
	ui.Success("The client tool is: " + client.Text)
	if info.Flavor == engine.FlavorMySQL && client.MariaDB {
		ui.Warn("The server is MySQL, but the client tool is from MariaDB. Install the MySQL client if the backup fails.")
	}
	return nil
}

func (s *session) reportOldClient(tooOld *engine.ClientTooOldError, lines []string) error {
	ui := s.env.UI
	ui.Error(app.Sentence(tooOld.Error()))
	ui.Info(fmt.Sprintf("To install the PostgreSQL %d client tools from the PGDG repository, run these commands:", tooOld.Server))
	ui.Code(strings.Join(lines, "\n"))
	return fmt.Errorf("pg_dump %d cannot make a backup of a PostgreSQL %d server. Install the new client tools, then run dbak init again", tooOld.Client, tooOld.Server)
}

func (s *session) compatibleClient(ctx context.Context, eng engine.Full, info engine.ServerInfo) (engine.Full, error) {
	var tooOld *engine.ClientTooOldError
	if err := s.checkClient(ctx, eng, info); !errors.As(err, &tooOld) {
		return eng, err
	}
	install, ok := system.PGDGClientInstall(s.env.OSRelease, runtime.GOARCH, tooOld.Server)
	if !ok {
		return nil, s.reportOldClient(tooOld, system.PGDGInstructions(s.env.OSFamily, tooOld.Server))
	}
	if err := s.installPGDGClient(ctx, tooOld, install); err != nil {
		return nil, err
	}
	eng, err := s.env.NewEngine(*s.src(), s.password())
	if err != nil {
		return nil, err
	}
	if err := s.checkClient(ctx, eng, info); err != nil {
		return nil, fmt.Errorf("the client tool in %s does not work with the server: %w. Install the client tools manually, then run dbak init again", install.BinDir, err)
	}
	return eng, nil
}

func (s *session) installPGDGClient(ctx context.Context, tooOld *engine.ClientTooOldError, install system.PGDGInstall) error {
	ui := s.env.UI
	lines := install.Lines()
	ui.Warn(fmt.Sprintf("pg_dump %d cannot make a backup of a PostgreSQL %d server.", tooOld.Client, tooOld.Server))
	confirmed, err := ui.Confirm(fmt.Sprintf("Install the PostgreSQL %d client tools from the PGDG repository now?", tooOld.Server),
		"dbak will run: "+strings.Join(lines, " && "), true)
	if err != nil {
		return err
	}
	if !confirmed {
		return s.reportOldClient(tooOld, lines)
	}
	for _, c := range install.Commands {
		if err := s.env.RunCommand(ctx, c); err != nil {
			return installFailed(c, err, tooOld.Server, install.RemoveRepo)
		}
	}
	src := s.src()
	src.BinDir = install.BinDir
	tool := backup.ClientTool(*src)
	if _, err := s.env.LookPath(tool); err != nil {
		return fmt.Errorf("the client tool %s does not exist after the install. Install the PostgreSQL %d client tools manually, then run dbak init again", tool, tooOld.Server)
	}
	ui.Success(fmt.Sprintf("The PostgreSQL %d client tools are installed. dbak will use %s.", tooOld.Server, tool))
	return nil
}

func installFailed(command []string, err error, major int, removeRepo string) error {
	message := fmt.Sprintf("the command %q failed: %v. Install the PostgreSQL %d client tools manually, then run dbak init again", strings.Join(command, " "), err, major)
	if removeRepo != "" {
		message += ". If dnf fails after this error, remove the PGDG repository with the command: " + removeRepo
	}
	return errors.New(message)
}

func (s *session) chooseDatabases(ctx context.Context) error {
	all, err := s.eng.ListDatabases(ctx)
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return errors.New("the user cannot see a database on the server. Grant access to a database, then run dbak init again")
	}
	selected := intersect(s.src().Databases, all)
	if len(selected) == 0 {
		for _, db := range all {
			if !(s.src().Engine == config.EnginePostgres && db == "postgres") {
				selected = append(selected, db)
			}
		}
	}
	chosen, err := s.env.UI.MultiSelect("Select the databases for backup.", "",
		sameOptions(all...), selected, func(v []string) error {
			if len(v) == 0 {
				return errors.New("select one or more databases")
			}
			return nil
		})
	if err != nil {
		return err
	}
	invalid := slices.DeleteFunc(slices.Clone(chosen), config.ValidName)
	if len(invalid) > 0 {
		return fmt.Errorf("dbak cannot make a backup of a database with the name %q. Use only databases with letters, digits, and underscores in the name", invalid[0])
	}
	s.src().Databases = chosen
	return nil
}

func intersect(wanted, available []string) []string {
	var out []string
	for _, w := range wanted {
		if slices.Contains(available, w) {
			out = append(out, w)
		}
	}
	return out
}

func (s *session) dumpCheck(ctx context.Context) error {
	ui := s.env.UI
	ui.Heading("Dump check")
	for {
		problems := 0
		for _, db := range s.src().Databases {
			if !s.checkDatabase(ctx, db) {
				problems++
			}
		}
		if problems == 0 {
			return nil
		}
		choice, err := ui.Select("The dump check found problems. What do you want to do?", "",
			options("Run the check again", "retry", "Continue without a good check", "continue", "Stop dbak init", "stop"), "retry")
		if err != nil {
			return err
		}
		switch choice {
		case "continue":
			ui.Warn("The backup of some databases can fail.")
			return nil
		case "stop":
			return ErrAborted
		}
	}
}

func (s *session) checkDatabase(ctx context.Context, db string) bool {
	ui := s.env.UI
	ok := true
	report, err := s.eng.PrivilegeCheck(ctx, db)
	switch {
	case err != nil:
		ui.Error(fmt.Sprintf("Cannot examine the privileges in %s: %s", db, app.Sentence(err.Error())))
		ok = false
	case !report.OK():
		ui.Error(fmt.Sprintf("The backup user cannot read these objects in %s:", db))
		ui.Code(strings.Join(report.Problems, "\n"))
		ui.Info(fmt.Sprintf("To correct the privileges, run this SQL as a superuser in the database %s:", db))
		ui.Code(report.FixSQL)
		ok = false
	}
	if err := s.eng.SchemaCheck(ctx, db); err != nil {
		ui.Error(fmt.Sprintf("The schema dump of %s failed: %s", db, app.Sentence(err.Error())))
		ok = false
	}
	if ok {
		ui.Success(fmt.Sprintf("The dump check of %s passed.", db))
	}
	return ok
}

func required(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("type a value")
	}
	return nil
}

func validPort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("type a number from 1 to 65535")
	}
	return nil
}
