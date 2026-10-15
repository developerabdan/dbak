package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"dbak/internal/engine"
)

const DefaultBinary = "pg_dump"

type Postgres struct {
	opts engine.Options
}

func New(opts engine.Options) *Postgres {
	if opts.Binary == "" {
		opts.Binary = DefaultBinary
	}
	return &Postgres{opts: opts}
}

func (p *Postgres) Name() string {
	return "postgres"
}

func (p *Postgres) FileExtension() string {
	return "dump"
}

func (p *Postgres) WrapOutput(w io.Writer) (io.WriteCloser, error) {
	return engine.NopWriteCloser(w), nil
}

func (p *Postgres) Probe(ctx context.Context, database string) (engine.ServerInfo, error) {
	cfg, err := pgx.ParseConfig(p.connString(database))
	if err != nil {
		return engine.ServerInfo{}, fmt.Errorf("the PostgreSQL connection settings are not valid: %w", err)
	}
	cfg.User = p.opts.Username
	cfg.Password = p.opts.Password
	cfg.ConnectTimeout = 10 * time.Second
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return engine.ServerInfo{}, p.classify(err)
	}
	defer conn.Close(context.Background())
	var version string
	var versionNum int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version'), current_setting('server_version_num')::int").Scan(&version, &versionNum); err != nil {
		return engine.ServerInfo{}, fmt.Errorf("cannot read the PostgreSQL server version: %w", err)
	}
	return engine.ServerInfo{Flavor: engine.FlavorPostgres, Version: version, Major: versionNum / 10000}, nil
}

func (p *Postgres) connString(database string) string {
	return fmt.Sprintf("host=%s port=%d dbname=%s sslmode=%s",
		quote(p.opts.Host), p.opts.Port, quote(database), quote(p.opts.SSLMode))
}

func quote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	return "'" + value + "'"
}

func (p *Postgres) classify(err error) error {
	address := net.JoinHostPort(p.opts.Host, strconv.Itoa(p.opts.Port))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01", "28000":
			return &engine.ConnectError{Message: "PostgreSQL rejected the user name or password. Make sure that the credentials are correct", Err: err}
		case "3D000":
			return &engine.ConnectError{Message: "the database does not exist on the PostgreSQL server. Make sure that the database name is correct", Err: err}
		}
		return &engine.ConnectError{Message: fmt.Sprintf("PostgreSQL rejected the connection: %s. Correct the cause and try again", pgErr.Message), Err: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
		return &engine.ConnectError{Message: fmt.Sprintf("cannot connect to %s. Make sure that the host is correct and the port is open", address), Err: err}
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "ssl") || strings.Contains(lower, "tls") || strings.Contains(lower, "certificate") {
		return &engine.ConnectError{Message: "the SSL connection to PostgreSQL failed. Examine the ssl_mode value and the server certificate", Err: err}
	}
	if strings.Contains(lower, "connect") {
		return &engine.ConnectError{Message: fmt.Sprintf("cannot connect to %s. Make sure that the host is correct and the port is open", address), Err: err}
	}
	return &engine.ConnectError{Message: "the connection to PostgreSQL failed. Examine the host, the port, and the credentials", Err: err}
}

func (p *Postgres) DumpCommand(ctx context.Context, database string) (*engine.Dump, error) {
	passFile, err := engine.WriteSecretFile(p.opts.TempDir, "pgpass-*", p.pgpassLine())
	if err != nil {
		return nil, fmt.Errorf("cannot write a temporary password file in %s: %w. Make sure that the dbak user can write to the directory", p.opts.TempDir, err)
	}
	cmd := exec.CommandContext(ctx, p.opts.Binary, p.dumpArgs(database)...)
	cmd.Env = append(engine.EnvWithout(os.Environ(), "PGPASSWORD", "PGPASSFILE", "PGSSLMODE"),
		"PGPASSFILE="+passFile,
		"PGSSLMODE="+p.opts.SSLMode,
	)
	return engine.NewDump(cmd, engine.RemoveFile(passFile)), nil
}

func (p *Postgres) dumpArgs(database string) []string {
	return []string{
		"--format=custom",
		"--compress=6",
		"--no-password",
		"--host", p.opts.Host,
		"--port", strconv.Itoa(p.opts.Port),
		"--username", p.opts.Username,
		"--dbname", database,
	}
}

func (p *Postgres) pgpassLine() string {
	fields := []string{p.opts.Host, strconv.Itoa(p.opts.Port), "*", p.opts.Username, p.opts.Password}
	for i, field := range fields {
		if i == 2 {
			continue
		}
		fields[i] = escapePgpass(field)
	}
	return strings.Join(fields, ":") + "\n"
}

func escapePgpass(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, ":", `\:`)
}
