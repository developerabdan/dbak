//go:build integration

package engine_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"dbak/internal/config"
	"dbak/internal/engine"
	"dbak/internal/engine/mysql"
	"dbak/internal/engine/postgres"
	"dbak/internal/sqlhelper"
)

const (
	testPassword = "Te:st\\pa\"ss#1"
	rootPassword = "rootpass"
)

type server struct {
	name    string
	engine  string
	port    int
	binEnv  string
	variant sqlhelper.Variant
}

var servers = []server{
	{"postgres 16", config.EnginePostgres, 55416, "DBAK_IT_PG_BIN", sqlhelper.Postgres14},
	{"postgres 13", config.EnginePostgres, 55413, "DBAK_IT_PG_BIN", sqlhelper.Postgres13},
	{"mysql 8.0", config.EngineMySQL, 53380, "DBAK_IT_MYSQL_BIN", sqlhelper.MySQL8020},
	{"mariadb 10.11", config.EngineMySQL, 53310, "DBAK_IT_MARIADB_BIN", sqlhelper.MariaDBOlder},
}

func binary(t *testing.T, s server) string {
	dir := os.Getenv(s.binEnv)
	name := postgres.DefaultBinary
	if s.engine == config.EngineMySQL {
		name = mysql.DefaultBinary
	}
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func newEngine(t *testing.T, s server, user, password string) engine.Full {
	opts := engine.Options{
		Host:     "127.0.0.1",
		Port:     s.port,
		Username: user,
		Password: password,
		SSLMode:  "disable",
		TempDir:  t.TempDir(),
		Binary:   binary(t, s),
	}
	if s.engine == config.EngineMySQL {
		opts.SSLMode = "preferred"
		return mysql.New(opts)
	}
	return postgres.New(opts)
}

func available(t *testing.T, e engine.Full) engine.ServerInfo {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := e.ProbeServer(ctx)
	if err != nil {
		t.Skipf("the server is not available: %v", err)
	}
	return info
}

func TestIntegrationInspector(t *testing.T) {
	ctx := context.Background()
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			e := newEngine(t, s, "backup_ro", testPassword)
			info := available(t, e)
			databases, err := e.ListDatabases(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(databases, "app_prod") {
				t.Fatalf("app_prod is not in %v", databases)
			}
			for _, hidden := range []string{"template0", "template1", "mysql", "information_schema", "sys", "performance_schema"} {
				if slices.Contains(databases, hidden) {
					t.Fatalf("%s is in %v", hidden, databases)
				}
			}
			client, err := e.ClientVersion(ctx)
			if err != nil || client.Major == 0 {
				t.Fatalf("got %+v %v", client, err)
			}
			if s.engine == config.EnginePostgres {
				if err := postgres.CheckClient(client, info); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.SchemaCheck(ctx, "app_prod"); err != nil {
				t.Fatal(err)
			}
			report, err := e.PrivilegeCheck(ctx, "app_prod")
			if err != nil || !report.OK() {
				t.Fatalf("got %+v %v", report, err)
			}
		})
	}
}

func TestIntegrationPrivilegeCheckFindsTableAndFixWorks(t *testing.T) {
	ctx := context.Background()
	s := servers[1]
	e := newEngine(t, s, "backup_ro", testPassword)
	available(t, e)
	report, err := e.PrivilegeCheck(ctx, "app_restricted")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(report.Problems, []string{`"public"."audit_log"`}) {
		t.Fatalf("got %v", report.Problems)
	}
	if err := e.SchemaCheck(ctx, "app_restricted"); err == nil {
		t.Fatal("the schema check passed without SELECT on audit_log")
	}
	root := postgresRoot(t, s.port, "app_restricted")
	if _, err := root.Exec(ctx, report.FixSQL); err != nil {
		t.Fatalf("the fix SQL failed: %v", err)
	}
	defer root.Exec(ctx, "REVOKE SELECT ON public.audit_log FROM backup_ro")
	fixed, err := e.PrivilegeCheck(ctx, "app_restricted")
	if err != nil || !fixed.OK() {
		t.Fatalf("got %+v %v after the fix", fixed, err)
	}
}

func TestIntegrationMySQLPrivilegeFix(t *testing.T) {
	ctx := context.Background()
	s := servers[2]
	available(t, newEngine(t, s, "backup_ro", testPassword))
	root := mysqlRoot(t, s.port)
	mustExec(t, root, "CREATE USER 'partial_ro'@'%' IDENTIFIED BY 'Partial123'")
	defer root.Exec("DROP USER 'partial_ro'@'%'")
	mustExec(t, root, "GRANT SELECT ON `app_prod`.* TO 'partial_ro'@'%'")
	e := newEngine(t, s, "partial_ro", "Partial123")
	report, err := e.PrivilegeCheck(ctx, "app_prod")
	if err != nil || report.OK() {
		t.Fatalf("got %+v %v, want missing privileges", report, err)
	}
	mustExec(t, root, strings.TrimSpace(report.FixSQL))
	fixed, err := e.PrivilegeCheck(ctx, "app_prod")
	if err != nil || !fixed.OK() {
		t.Fatalf("got %+v %v after the fix", fixed, err)
	}
}

func TestIntegrationHelperSQLMakesWorkingUser(t *testing.T) {
	ctx := context.Background()
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			available(t, newEngine(t, s, "backup_ro", testPassword))
			password, err := sqlhelper.GeneratePassword()
			if err != nil {
				t.Fatal(err)
			}
			script, err := sqlhelper.Render(sqlhelper.Request{
				Variant:   s.variant,
				User:      "helper_ro",
				Password:  password,
				Host:      "%",
				Databases: []string{"app_prod"},
				Schemas:   []string{"public"},
				OwnerRole: "postgres",
			})
			if err != nil {
				t.Fatal(err)
			}
			if s.engine == config.EnginePostgres {
				runPostgresScript(t, s, script)
				defer dropPostgresUser(t, s)
			} else {
				root := mysqlRoot(t, s.port)
				for _, statement := range strings.Split(strings.TrimSpace(script.Steps[0].SQL), ";\n") {
					mustExec(t, root, strings.TrimSuffix(statement, ";"))
				}
				defer root.Exec("DROP USER 'helper_ro'@'%'")
			}
			e := newEngine(t, s, "helper_ro", password)
			if err := e.SchemaCheck(ctx, "app_prod"); err != nil {
				t.Fatalf("the schema check failed for the new user: %v", err)
			}
			report, err := e.PrivilegeCheck(ctx, "app_prod")
			if err != nil || !report.OK() {
				t.Fatalf("got %+v %v for the new user", report, err)
			}
		})
	}
}

func runPostgresScript(t *testing.T, s server, script sqlhelper.Script) {
	t.Helper()
	ctx := context.Background()
	for _, step := range script.Steps {
		if strings.Contains(step.SQL, "tableowner") {
			continue
		}
		database := "postgres"
		if strings.Contains(step.Instruction, "database app_prod") {
			database = "app_prod"
		}
		conn := postgresRoot(t, s.port, database)
		if _, err := conn.Exec(ctx, step.SQL); err != nil {
			t.Fatalf("the helper SQL failed in %s: %v\n%s", database, err, step.SQL)
		}
	}
}

func dropPostgresUser(t *testing.T, s server) {
	ctx := context.Background()
	conn := postgresRoot(t, s.port, "app_prod")
	conn.Exec(ctx, "DROP OWNED BY helper_ro")
	other := postgresRoot(t, s.port, "postgres")
	other.Exec(ctx, "DROP OWNED BY helper_ro")
	other.Exec(ctx, "DROP ROLE helper_ro")
}

func postgresRoot(t *testing.T, port int, database string) *pgx.Conn {
	t.Helper()
	url := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%d/%s?sslmode=disable", rootPassword, port, database)
	conn, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func mysqlRoot(t *testing.T, port int) *sql.DB {
	t.Helper()
	cfg := driver.NewConfig()
	cfg.User = "root"
	cfg.Passwd = rootPassword
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("127.0.0.1:%d", port)
	connector, err := driver.NewConnector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })
	return db
}

func mustExec(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}
