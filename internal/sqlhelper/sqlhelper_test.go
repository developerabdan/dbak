package sqlhelper

import (
	"strings"
	"testing"

	"dbak/internal/config"
)

const testPassword = "Abc123Abc123Abc123Abc123Abc123Ab"

func TestRenderPostgres14(t *testing.T) {
	script, err := Render(Request{Variant: Postgres14, User: "backup_ro", Password: testPassword, Databases: []string{"app_prod"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE ROLE backup_ro WITH LOGIN PASSWORD '" + testPassword + "';\nGRANT pg_read_all_data TO backup_ro;\n"
	if len(script.Steps) != 1 || script.Steps[0].SQL != want {
		t.Fatalf("got %q, want %q", script.Steps[0].SQL, want)
	}
}

func TestRenderPostgres13(t *testing.T) {
	script, err := Render(Request{
		Variant:   Postgres13,
		User:      "backup_ro",
		Password:  testPassword,
		Databases: []string{"app_prod", "app_audit"},
		Schemas:   []string{"public", "billing"},
		OwnerRole: "app_owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(script.Steps) != 4 {
		t.Fatalf("got %d steps, want 4", len(script.Steps))
	}
	connect := script.Steps[0].SQL
	for _, want := range []string{
		"CREATE ROLE backup_ro WITH LOGIN PASSWORD",
		"GRANT CONNECT ON DATABASE app_prod TO backup_ro;\n",
		"GRANT CONNECT ON DATABASE app_audit TO backup_ro;\n",
	} {
		if !strings.Contains(connect, want) {
			t.Fatalf("%q is missing in %q", want, connect)
		}
	}
	if !strings.Contains(script.Steps[1].Instruction, "app_prod") || !strings.Contains(script.Steps[2].Instruction, "app_audit") {
		t.Fatal("the steps do not name the databases")
	}
	schema := script.Steps[1].SQL
	for _, want := range []string{
		"GRANT USAGE ON SCHEMA billing TO backup_ro;\n",
		"GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO backup_ro;\n",
		"ALTER DEFAULT PRIVILEGES FOR ROLE app_owner IN SCHEMA billing GRANT SELECT ON TABLES TO backup_ro;\n",
	} {
		if !strings.Contains(schema, want) {
			t.Fatalf("%q is missing in %q", want, schema)
		}
	}
	if !strings.Contains(script.Steps[3].SQL, "tableowner") {
		t.Fatal("the owner query is missing")
	}
	if len(script.Warnings) != 1 || !strings.Contains(script.Warnings[0], "app_owner") {
		t.Fatalf("unexpected warnings: %v", script.Warnings)
	}
}

func TestRenderMySQLVariants(t *testing.T) {
	cases := []struct {
		variant Variant
		want    string
		absent  []string
	}{
		{MySQL8020, "GRANT SHOW_ROUTINE ON *.* TO 'backup_ro'@'10.0.0.5';", []string{"mysql`.`proc", "SHOW CREATE ROUTINE"}},
		{MySQL80, "", []string{"SHOW_ROUTINE", "mysql`.`proc", "SHOW CREATE ROUTINE"}},
		{MySQL57, "GRANT SELECT ON `mysql`.`proc` TO 'backup_ro'@'10.0.0.5';", []string{"SHOW_ROUTINE"}},
		{MariaDBOlder, "GRANT SELECT ON `mysql`.`proc` TO 'backup_ro'@'10.0.0.5';", []string{"SHOW_ROUTINE"}},
		{MariaDB113, "GRANT SHOW CREATE ROUTINE ON *.* TO 'backup_ro'@'10.0.0.5';", []string{"mysql`.`proc"}},
	}
	for _, c := range cases {
		t.Run(c.variant.Label, func(t *testing.T) {
			script, err := Render(Request{Variant: c.variant, User: "backup_ro", Password: testPassword, Host: "10.0.0.5", Databases: []string{"app_prod", "shop"}})
			if err != nil {
				t.Fatal(err)
			}
			sql := script.Steps[0].SQL
			for _, want := range []string{
				"CREATE USER 'backup_ro'@'10.0.0.5' IDENTIFIED BY '" + testPassword + "';\n",
				"GRANT SELECT, SHOW VIEW, TRIGGER, LOCK TABLES, EVENT ON `app_prod`.* TO 'backup_ro'@'10.0.0.5';\n",
				"GRANT SELECT, SHOW VIEW, TRIGGER, LOCK TABLES, EVENT ON `shop`.* TO 'backup_ro'@'10.0.0.5';\n",
				c.want,
			} {
				if !strings.Contains(sql, want) {
					t.Fatalf("%q is missing in %q", want, sql)
				}
			}
			for _, absent := range c.absent {
				if strings.Contains(sql, absent) {
					t.Fatalf("%q is in %q", absent, sql)
				}
			}
		})
	}
}

func TestWildcardHostGivesWarning(t *testing.T) {
	script, err := Render(Request{Variant: MySQL8020, User: "backup_ro", Password: testPassword, Host: "%", Databases: []string{"app_prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(script.Warnings) != 1 || !strings.Contains(script.Warnings[0], "all hosts") {
		t.Fatalf("unexpected warnings: %v", script.Warnings)
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	base := Request{Variant: MySQL8020, User: "backup_ro", Password: testPassword, Host: "10.0.0.5", Databases: []string{"app_prod"}}
	cases := map[string]func(r *Request){
		"user with a quote":      func(r *Request) { r.User = "x'; DROP USER root; --" },
		"database with a dash":   func(r *Request) { r.Databases = []string{"app-prod"} },
		"database with backtick": func(r *Request) { r.Databases = []string{"a`b"} },
		"empty database list":    func(r *Request) { r.Databases = nil },
		"host with a quote":      func(r *Request) { r.Host = "10.0.0.5'" },
		"password with a quote":  func(r *Request) { r.Password = "a'b" },
		"postgres 13 bad schema": func(r *Request) { r.Variant = Postgres13; r.OwnerRole = "postgres"; r.Schemas = []string{"pub lic"} },
		"postgres 13 bad owner":  func(r *Request) { r.Variant = Postgres13; r.OwnerRole = "a;b"; r.Schemas = []string{"public"} },
		"unknown variant":        func(r *Request) { r.Variant = Variant{} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := base
			change(&r)
			if _, err := Render(r); err == nil {
				t.Fatal("got no error, want an error")
			}
		})
	}
}

func TestGeneratePassword(t *testing.T) {
	first, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != PasswordLength || first == second {
		t.Fatalf("got %q and %q", first, second)
	}
	for _, r := range first {
		if !strings.ContainsRune(passwordAlphabet, r) {
			t.Fatalf("character %q is not alphanumeric", r)
		}
	}
}

func TestVariants(t *testing.T) {
	if len(Variants(config.EnginePostgres)) != 2 || len(Variants(config.EngineMySQL)) != 5 {
		t.Fatal("unexpected variant count")
	}
	if v, ok := VariantByID("mariadb-11.3"); !ok || v != MariaDB113 {
		t.Fatal("cannot find a variant by id")
	}
}

func TestSplitList(t *testing.T) {
	got := SplitList("app_prod, app_audit\napp_prod  shop")
	want := []string{"app_prod", "app_audit", "shop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}
