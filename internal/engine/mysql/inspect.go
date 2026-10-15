package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"dbak/internal/engine"
)

var systemDatabases = []string{"information_schema", "performance_schema", "sys", "mysql"}

var RequiredPrivileges = []string{"SELECT", "SHOW VIEW", "TRIGGER", "LOCK TABLES", "EVENT"}

var (
	grantPattern         = regexp.MustCompile(`^GRANT (.+?) ON (\S+) TO `)
	clientVersionPattern = regexp.MustCompile(`(?:Distrib|from) (\d+)\.|Ver (\d+)\.`)
)

func (m *MySQL) ProbeServer(ctx context.Context) (engine.ServerInfo, error) {
	return m.Probe(ctx, "")
}

func (m *MySQL) ListDatabases(ctx context.Context) ([]string, error) {
	db, err := m.open("")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, m.classify(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if !slices.Contains(systemDatabases, strings.ToLower(name)) {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

func (m *MySQL) ClientVersion(ctx context.Context) (engine.ClientInfo, error) {
	out, err := engine.ToolOutput(ctx, m.opts.Binary, "--version")
	if err != nil {
		return engine.ClientInfo{}, err
	}
	return ParseClientVersion(out), nil
}

func ParseClientVersion(text string) engine.ClientInfo {
	info := engine.ClientInfo{Text: text, MariaDB: IsMariaDBClient(text)}
	if m := clientVersionPattern.FindStringSubmatch(text); m != nil {
		major := m[1]
		if major == "" {
			major = m[2]
		}
		info.Major, _ = strconv.Atoi(major)
	}
	return info
}

func (m *MySQL) SchemaCheck(ctx context.Context, database string) error {
	dump, err := m.dumpCommand(ctx, database, "--no-data")
	if err != nil {
		return err
	}
	defer dump.Cleanup()
	return engine.RunCheck(dump.Cmd)
}

func (m *MySQL) PrivilegeCheck(ctx context.Context, database string) (engine.PrivilegeReport, error) {
	db, err := m.open("")
	if err != nil {
		return engine.PrivilegeReport{}, err
	}
	defer db.Close()
	var account string
	if err := db.QueryRowContext(ctx, "SELECT CURRENT_USER()").Scan(&account); err != nil {
		return engine.PrivilegeReport{}, m.classify(err)
	}
	grants, err := showGrants(ctx, db)
	if err != nil {
		return engine.PrivilegeReport{}, err
	}
	missing := MissingPrivileges(grants, database)
	if len(missing) == 0 {
		return engine.PrivilegeReport{}, nil
	}
	user, host := splitAccount(account)
	return engine.PrivilegeReport{
		Problems: []string{fmt.Sprintf("The user does not have these privileges on %s: %s.", database, strings.Join(missing, ", "))},
		FixSQL:   fmt.Sprintf("GRANT %s ON `%s`.* TO '%s'@'%s';\n", strings.Join(missing, ", "), database, user, host),
	}, nil
}

func showGrants(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SHOW GRANTS")
	if err != nil {
		return nil, fmt.Errorf("cannot read the grants of the user: %w", err)
	}
	defer rows.Close()
	var grants []string
	for rows.Next() {
		var grant string
		if err := rows.Scan(&grant); err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func MissingPrivileges(grants []string, database string) []string {
	have := map[string]bool{}
	for _, grant := range grants {
		match := grantPattern.FindStringSubmatch(grant)
		if match == nil || !scopeCovers(match[2], database) {
			continue
		}
		for _, privilege := range strings.Split(match[1], ",") {
			have[strings.ToUpper(strings.TrimSpace(privilege))] = true
		}
	}
	if have["ALL PRIVILEGES"] || have["ALL"] {
		return nil
	}
	var missing []string
	for _, privilege := range RequiredPrivileges {
		if !have[privilege] {
			missing = append(missing, privilege)
		}
	}
	return missing
}

func scopeCovers(scope, database string) bool {
	if scope == "*.*" {
		return true
	}
	dbPart, tablePart, ok := strings.Cut(scope, ".")
	if !ok || tablePart != "*" {
		return false
	}
	name := strings.Trim(dbPart, "`")
	name = strings.NewReplacer(`\_`, "_", `\%`, "%").Replace(name)
	return name == database
}

func splitAccount(account string) (string, string) {
	i := strings.LastIndex(account, "@")
	if i < 0 {
		return account, "%"
	}
	return account[:i], account[i+1:]
}
