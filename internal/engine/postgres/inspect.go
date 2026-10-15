package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/developerabdan/dbak/internal/engine"
)

var maintenanceDatabases = []string{"postgres", "template1"}

var clientVersionPattern = regexp.MustCompile(`\)\s+(\d+)(?:\.(\d+))?`)

const unreadableQuery = `
SELECT n.nspname, c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'S', 'm')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp%'
  AND (NOT has_schema_privilege(n.oid, 'USAGE') OR NOT has_table_privilege(c.oid, 'SELECT'))
ORDER BY n.nspname, c.relname`

func (p *Postgres) connect(ctx context.Context, database string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(p.connString(database))
	if err != nil {
		return nil, fmt.Errorf("the PostgreSQL connection settings are not valid: %w", err)
	}
	cfg.User = p.opts.Username
	cfg.Password = p.opts.Password
	cfg.ConnectTimeout = 10 * time.Second
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, p.classify(err)
	}
	return conn, nil
}

func (p *Postgres) connectMaintenance(ctx context.Context) (*pgx.Conn, error) {
	var lastErr error
	for _, db := range maintenanceDatabases {
		conn, err := p.connect(ctx, db)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (p *Postgres) ProbeServer(ctx context.Context) (engine.ServerInfo, error) {
	conn, err := p.connectMaintenance(ctx)
	if err != nil {
		return engine.ServerInfo{}, err
	}
	defer conn.Close(context.Background())
	return serverInfo(ctx, conn)
}

func serverInfo(ctx context.Context, conn *pgx.Conn) (engine.ServerInfo, error) {
	var version string
	var versionNum int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version'), current_setting('server_version_num')::int").Scan(&version, &versionNum); err != nil {
		return engine.ServerInfo{}, fmt.Errorf("cannot read the PostgreSQL server version: %w", err)
	}
	return engine.ServerInfo{Flavor: engine.FlavorPostgres, Version: version, Major: versionNum / 10000}, nil
}

func (p *Postgres) ListDatabases(ctx context.Context) ([]string, error) {
	conn, err := p.connectMaintenance(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.Background())
	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname")
	if err != nil {
		return nil, fmt.Errorf("cannot list the databases: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (p *Postgres) ClientVersion(ctx context.Context) (engine.ClientInfo, error) {
	out, err := engine.ToolOutput(ctx, p.opts.Binary, "--version")
	if err != nil {
		return engine.ClientInfo{}, err
	}
	return ParseClientVersion(out)
}

func ParseClientVersion(text string) (engine.ClientInfo, error) {
	m := clientVersionPattern.FindStringSubmatch(text)
	if m == nil {
		return engine.ClientInfo{}, fmt.Errorf("cannot read the pg_dump version from %q", text)
	}
	major, _ := strconv.Atoi(m[1])
	return engine.ClientInfo{Text: text, Major: major}, nil
}

func CheckClient(client engine.ClientInfo, server engine.ServerInfo) error {
	if client.Major < server.Major {
		return &engine.ClientTooOldError{Tool: DefaultBinary, Client: client.Major, Server: server.Major}
	}
	return nil
}

func (p *Postgres) SchemaCheck(ctx context.Context, database string) error {
	cmd, cleanup, err := p.command(ctx, database, "--schema-only")
	if err != nil {
		return err
	}
	defer cleanup()
	return engine.RunCheck(cmd)
}

func (p *Postgres) command(ctx context.Context, database string, extra ...string) (*exec.Cmd, func() error, error) {
	passFile, err := engine.WriteSecretFile(p.opts.TempDir, "pgpass-*", p.pgpassLine())
	if err != nil {
		return nil, nil, fmt.Errorf("cannot write a temporary password file in %s: %w. Make sure that the dbak user can write to the directory", p.opts.TempDir, err)
	}
	cmd := exec.CommandContext(ctx, p.opts.Binary, append(p.dumpArgs(database), extra...)...)
	cmd.Env = append(engine.EnvWithout(os.Environ(), "PGPASSWORD", "PGPASSFILE", "PGSSLMODE"),
		"PGPASSFILE="+passFile,
		"PGSSLMODE="+p.opts.SSLMode,
	)
	return cmd, engine.RemoveFile(passFile), nil
}

func (p *Postgres) PrivilegeCheck(ctx context.Context, database string) (engine.PrivilegeReport, error) {
	conn, err := p.connect(ctx, database)
	if err != nil {
		return engine.PrivilegeReport{}, err
	}
	defer conn.Close(context.Background())
	rows, err := conn.Query(ctx, unreadableQuery)
	if err != nil {
		return engine.PrivilegeReport{}, fmt.Errorf("cannot read the table privileges in %s: %w", database, err)
	}
	var report engine.PrivilegeReport
	var schemas []string
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			return engine.PrivilegeReport{}, err
		}
		report.Problems = append(report.Problems, pgx.Identifier{schema, table}.Sanitize())
		if len(schemas) == 0 || schemas[len(schemas)-1] != schema {
			schemas = append(schemas, schema)
		}
	}
	if err := rows.Err(); err != nil {
		return engine.PrivilegeReport{}, fmt.Errorf("cannot read the table privileges in %s: %w", database, err)
	}
	report.FixSQL = grantFix(schemas, p.opts.Username)
	return report, nil
}

func grantFix(schemas []string, user string) string {
	var b strings.Builder
	role := pgx.Identifier{user}.Sanitize()
	for _, schema := range schemas {
		s := pgx.Identifier{schema}.Sanitize()
		fmt.Fprintf(&b, "GRANT USAGE ON SCHEMA %s TO %s;\n", s, role)
		fmt.Fprintf(&b, "GRANT SELECT ON ALL TABLES IN SCHEMA %s TO %s;\n", s, role)
		fmt.Fprintf(&b, "GRANT SELECT ON ALL SEQUENCES IN SCHEMA %s TO %s;\n", s, role)
	}
	return b.String()
}
