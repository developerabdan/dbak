package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/klauspost/compress/zstd"

	"github.com/developerabdan/dbak/internal/engine"
)

const DefaultBinary = "mysqldump"

const zstdLevel = 3

type MySQL struct {
	opts        engine.Options
	server      engine.ServerInfo
	clientOnce  sync.Once
	clientMaria bool
}

func New(opts engine.Options) *MySQL {
	if opts.Binary == "" {
		opts.Binary = DefaultBinary
	}
	opts.SSLMode = strings.ToLower(opts.SSLMode)
	return &MySQL{opts: opts}
}

func (m *MySQL) Name() string {
	return "mysql"
}

func (m *MySQL) FileExtension() string {
	return "sql.zst"
}

func (m *MySQL) WrapOutput(w io.Writer) (io.WriteCloser, error) {
	return zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(zstdLevel)))
}

func (m *MySQL) open(database string) (*sql.DB, error) {
	cfg := driver.NewConfig()
	cfg.User = m.opts.Username
	cfg.Passwd = m.opts.Password
	cfg.Net = "tcp"
	cfg.Addr = m.address()
	cfg.DBName = database
	cfg.Timeout = 10 * time.Second
	cfg.TLSConfig = driverTLS(m.opts.SSLMode)
	connector, err := driver.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("the MySQL connection settings are not valid: %w", err)
	}
	return sql.OpenDB(connector), nil
}

func (m *MySQL) Probe(ctx context.Context, database string) (engine.ServerInfo, error) {
	db, err := m.open(database)
	if err != nil {
		return engine.ServerInfo{}, err
	}
	defer db.Close()
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return engine.ServerInfo{}, m.classify(err)
	}
	info := ParseServerVersion(version)
	if info.Flavor == engine.FlavorMySQL {
		var gtidMode string
		if err := db.QueryRowContext(ctx, "SELECT @@GLOBAL.gtid_mode").Scan(&gtidMode); err == nil {
			info.GTIDEnabled = !strings.EqualFold(gtidMode, "OFF")
		}
	}
	m.server = info
	return info, nil
}

func ParseServerVersion(version string) engine.ServerInfo {
	info := engine.ServerInfo{Flavor: engine.FlavorMySQL, Version: version}
	if strings.Contains(strings.ToLower(version), "mariadb") {
		info.Flavor = engine.FlavorMariaDB
	}
	majorText, _, _ := strings.Cut(version, ".")
	info.Major, _ = strconv.Atoi(majorText)
	return info
}

func driverTLS(mode string) string {
	switch mode {
	case "disabled":
		return "false"
	case "required":
		return "skip-verify"
	case "verify_ca", "verify_identity":
		return "true"
	default:
		return "preferred"
	}
}

func (m *MySQL) address() string {
	return net.JoinHostPort(m.opts.Host, strconv.Itoa(m.opts.Port))
}

func (m *MySQL) classify(err error) error {
	var myErr *driver.MySQLError
	if errors.As(err, &myErr) {
		switch myErr.Number {
		case 1045:
			return &engine.ConnectError{Message: "MySQL rejected the user name or password. Make sure that the credentials are correct", Err: err}
		case 1049:
			return &engine.ConnectError{Message: "the database does not exist on the MySQL server. Make sure that the database name is correct", Err: err}
		case 1044:
			return &engine.ConnectError{Message: "MySQL rejected access to the database. Grant the privileges from \"dbak user-helper\" to the user", Err: err}
		}
		return &engine.ConnectError{Message: fmt.Sprintf("MySQL rejected the connection: %s. Correct the cause and try again", myErr.Message), Err: err}
	}
	if errors.Is(err, driver.ErrNoTLS) {
		return &engine.ConnectError{Message: "the MySQL server does not support SSL. Change ssl_mode to preferred or disabled", Err: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
		return &engine.ConnectError{Message: fmt.Sprintf("cannot connect to %s. Make sure that the host is correct and the port is open", m.address()), Err: err}
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "tls") || strings.Contains(lower, "x509") || strings.Contains(lower, "certificate") {
		return &engine.ConnectError{Message: "the SSL connection to MySQL failed. Examine the ssl_mode value and the server certificate", Err: err}
	}
	return &engine.ConnectError{Message: "the connection to MySQL failed. Examine the host, the port, and the credentials", Err: err}
}

func (m *MySQL) DumpCommand(ctx context.Context, database string) (*engine.Dump, error) {
	return m.dumpCommand(ctx, database)
}

func (m *MySQL) dumpCommand(ctx context.Context, database string, extra ...string) (*engine.Dump, error) {
	if m.server.Flavor == "" {
		if _, err := m.Probe(ctx, ""); err != nil {
			return nil, err
		}
	}
	clientMaria := m.clientIsMariaDB(ctx)
	optionFile, err := engine.WriteSecretFile(m.opts.TempDir, "my-*.cnf", m.optionFile(clientMaria))
	if err != nil {
		return nil, fmt.Errorf("cannot write a temporary option file in %s: %w. Make sure that the dbak user can write to the directory", m.opts.TempDir, err)
	}
	cmd := exec.CommandContext(ctx, m.opts.Binary, m.dumpArgs(optionFile, database, clientMaria, extra...)...)
	cmd.Env = engine.EnvWithout(os.Environ(), "MYSQL_PWD")
	return engine.NewDump(cmd, engine.RemoveFile(optionFile)), nil
}

func (m *MySQL) dumpArgs(optionFile, database string, clientMaria bool, extra ...string) []string {
	args := []string{
		"--defaults-extra-file=" + optionFile,
		"--single-transaction",
		"--quick",
		"--routines",
		"--triggers",
		"--events",
		"--no-tablespaces",
	}
	if m.server.Flavor == engine.FlavorMySQL && m.server.GTIDEnabled && !clientMaria {
		args = append(args, "--set-gtid-purged=OFF")
	}
	args = append(args, extra...)
	return append(args, "--databases", database)
}

func (m *MySQL) optionFile(clientMaria bool) string {
	lines := []string{
		"[client]",
		"host=" + quoteOption(m.opts.Host),
		"port=" + strconv.Itoa(m.opts.Port),
		"user=" + quoteOption(m.opts.Username),
		"password=" + quoteOption(m.opts.Password),
	}
	if clientMaria {
		lines = append(lines, mariaSSLOptions(m.opts.SSLMode)...)
	} else {
		lines = append(lines, "ssl-mode="+strings.ToUpper(m.opts.SSLMode))
	}
	return strings.Join(lines, "\n") + "\n"
}

func mariaSSLOptions(mode string) []string {
	switch mode {
	case "disabled":
		return []string{"skip-ssl"}
	case "required":
		return []string{"ssl", "skip-ssl-verify-server-cert"}
	case "verify_ca", "verify_identity":
		return []string{"ssl", "ssl-verify-server-cert"}
	default:
		return []string{"skip-ssl-verify-server-cert"}
	}
}

func quoteOption(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + replacer.Replace(value) + `"`
}

func (m *MySQL) clientIsMariaDB(ctx context.Context) bool {
	m.clientOnce.Do(func() {
		out, err := exec.CommandContext(ctx, m.opts.Binary, "--version").Output()
		if err == nil {
			m.clientMaria = IsMariaDBClient(string(out))
		}
	})
	return m.clientMaria
}

func IsMariaDBClient(versionOutput string) bool {
	return strings.Contains(strings.ToLower(versionOutput), "mariadb")
}
