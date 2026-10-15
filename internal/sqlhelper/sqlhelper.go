package sqlhelper

import (
	"bytes"
	"crypto/rand"
	"embed"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"text/template"

	"github.com/developerabdan/dbak/internal/config"
)

//go:embed templates/*.sql.tmpl
var templateFiles embed.FS

var templates = template.Must(template.ParseFS(templateFiles, "templates/*.sql.tmpl"))

const (
	DefaultUser        = "backup_ro"
	DefaultOwnerRole   = "postgres"
	DefaultSchema      = "public"
	PasswordLength     = 32
	passwordAlphabet   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	VersionQuery       = "SELECT version();"
	routinesNone       = ""
	routinesShow       = "show_routine"
	routinesProc       = "mysql_proc"
	routinesShowCreate = "show_create_routine"
)

type Variant struct {
	ID     string
	Engine string
	Label  string
}

var (
	Postgres14   = Variant{"postgres-14", config.EnginePostgres, "PostgreSQL 14 or later"}
	Postgres13   = Variant{"postgres-13", config.EnginePostgres, "PostgreSQL 13 or earlier"}
	MySQL8020    = Variant{"mysql-8.0.20", config.EngineMySQL, "MySQL 8.0.20 or later"}
	MySQL80      = Variant{"mysql-8.0", config.EngineMySQL, "MySQL 8.0.0 to 8.0.19"}
	MySQL57      = Variant{"mysql-5.7", config.EngineMySQL, "MySQL 5.7"}
	MariaDB113   = Variant{"mariadb-11.3", config.EngineMySQL, "MariaDB 11.3 or later"}
	MariaDBOlder = Variant{"mariadb", config.EngineMySQL, "MariaDB 11.2 or earlier"}
)

func Variants(engine string) []Variant {
	var out []Variant
	for _, v := range []Variant{Postgres14, Postgres13, MySQL8020, MySQL80, MySQL57, MariaDB113, MariaDBOlder} {
		if v.Engine == engine {
			out = append(out, v)
		}
	}
	return out
}

func VariantByID(id string) (Variant, bool) {
	for _, v := range append(Variants(config.EnginePostgres), Variants(config.EngineMySQL)...) {
		if v.ID == id {
			return v, true
		}
	}
	return Variant{}, false
}

type Request struct {
	Variant   Variant
	User      string
	Password  string
	Host      string
	Databases []string
	Schemas   []string
	OwnerRole string
}

type Step struct {
	Instruction string
	SQL         string
}

type Script struct {
	Steps    []Step
	Warnings []string
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9._%:-]+$`)

func ValidHost(host string) bool {
	return hostPattern.MatchString(host)
}

func IsWildcardHost(host string) bool {
	return host == "%"
}

func (r Request) Validate() error {
	var problems []string
	if !config.ValidName(r.User) {
		problems = append(problems, fmt.Sprintf("the user name %q is not valid. Use only letters, digits, and underscores", r.User))
	}
	if r.Password == "" || strings.ContainsAny(r.Password, `'"\`) {
		problems = append(problems, "the password is empty or has a quote or a backslash. Use a generated password")
	}
	if len(r.Databases) == 0 {
		problems = append(problems, "the database list is empty. Type one or more database names")
	}
	for _, db := range r.Databases {
		if !config.ValidName(db) {
			problems = append(problems, fmt.Sprintf("the database name %q is not valid. Use only letters, digits, and underscores", db))
		}
	}
	switch r.Variant.Engine {
	case config.EngineMySQL:
		if !ValidHost(r.Host) {
			problems = append(problems, fmt.Sprintf("the host %q is not valid. Use an IP address, a host name, or %%", r.Host))
		}
	case config.EnginePostgres:
		if r.Variant == Postgres13 {
			if !config.ValidName(r.OwnerRole) {
				problems = append(problems, fmt.Sprintf("the owner role %q is not valid. Use only letters, digits, and underscores", r.OwnerRole))
			}
			if len(r.Schemas) == 0 {
				problems = append(problems, "the schema list is empty. Type one or more schema names")
			}
			for _, s := range r.Schemas {
				if !config.ValidName(s) {
					problems = append(problems, fmt.Sprintf("the schema name %q is not valid. Use only letters, digits, and underscores", s))
				}
			}
		}
	default:
		problems = append(problems, "the server version is not known. Select a server version")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func Render(r Request) (Script, error) {
	if err := r.Validate(); err != nil {
		return Script{}, err
	}
	switch r.Variant {
	case Postgres14:
		return renderPostgres14(r)
	case Postgres13:
		return renderPostgres13(r)
	}
	return renderMySQL(r)
}

func renderPostgres14(r Request) (Script, error) {
	sql, err := execute("postgres14.sql.tmpl", r)
	if err != nil {
		return Script{}, err
	}
	return Script{Steps: []Step{{Instruction: "Connect to the server as a superuser and run this SQL:", SQL: sql}}}, nil
}

func renderPostgres13(r Request) (Script, error) {
	connect, err := execute("postgres13_connect.sql.tmpl", r)
	if err != nil {
		return Script{}, err
	}
	schema, err := execute("postgres13_schema.sql.tmpl", r)
	if err != nil {
		return Script{}, err
	}
	owners, err := execute("postgres_owners.sql.tmpl", r)
	if err != nil {
		return Script{}, err
	}
	steps := []Step{{Instruction: "Connect to the server as a superuser and run this SQL:", SQL: connect}}
	for _, db := range r.Databases {
		steps = append(steps, Step{Instruction: fmt.Sprintf("Connect to the database %s as a superuser and run this SQL:", db), SQL: schema})
	}
	steps = append(steps, Step{Instruction: "To see the schemas and the table owners, run this SQL in each database:", SQL: owners})
	return Script{
		Steps: steps,
		Warnings: []string{fmt.Sprintf("The default privileges apply only to tables and sequences that the role %s makes. "+
			"If a different role makes tables, run the ALTER DEFAULT PRIVILEGES lines for that role also.", r.OwnerRole)},
	}, nil
}

type mysqlData struct {
	Request
	Routines string
}

func renderMySQL(r Request) (Script, error) {
	data := mysqlData{Request: r}
	var warnings []string
	switch r.Variant {
	case MySQL8020:
		data.Routines = routinesShow
	case MySQL80:
		data.Routines = routinesNone
		warnings = append(warnings, "MySQL before 8.0.20 shows routines only to a user with the global SELECT privilege. Without it, the backup does not contain stored procedures and functions.")
	case MySQL57, MariaDBOlder:
		data.Routines = routinesProc
	case MariaDB113:
		data.Routines = routinesShowCreate
	default:
		return Script{}, fmt.Errorf("the server version %q is not known", r.Variant.ID)
	}
	sql, err := execute("mysql.sql.tmpl", data)
	if err != nil {
		return Script{}, err
	}
	if IsWildcardHost(r.Host) {
		warnings = append(warnings, "The host % lets the user connect from all hosts. Use the IP address of this server if possible.")
	}
	return Script{
		Steps:    []Step{{Instruction: "Connect to the server as root or as an administrator and run this SQL:", SQL: sql}},
		Warnings: warnings,
	}, nil
}

func execute(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("cannot render the SQL template %s: %w", name, err)
	}
	return strings.TrimRight(buf.String(), "\n") + "\n", nil
}

func GeneratePassword() (string, error) {
	max := big.NewInt(int64(len(passwordAlphabet)))
	out := make([]byte, PasswordLength)
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("cannot make a random password: %w", err)
		}
		out[i] = passwordAlphabet[n.Int64()]
	}
	return string(out), nil
}

func SplitList(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func (s Script) Text() string {
	var b strings.Builder
	for i, step := range s.Steps {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(step.Instruction + "\n\n" + step.SQL)
	}
	return b.String()
}
