package mysql

import (
	"slices"
	"strings"
	"testing"

	"github.com/developerabdan/dbak/internal/engine"
)

func TestMissingPrivileges(t *testing.T) {
	cases := []struct {
		name   string
		grants []string
		want   []string
	}{
		{"all needed privileges on the database", []string{
			"GRANT USAGE ON *.* TO `backup_ro`@`%`",
			"GRANT SELECT, SHOW VIEW, TRIGGER, LOCK TABLES, EVENT ON `app_prod`.* TO `backup_ro`@`%`",
		}, nil},
		{"mysql 8 quote style", []string{
			"GRANT SELECT, LOCK TABLES, SHOW VIEW, EVENT, TRIGGER ON `app_prod`.* TO 'backup_ro'@'%'",
		}, nil},
		{"escaped underscore in the name", []string{
			"GRANT SELECT, SHOW VIEW, TRIGGER, LOCK TABLES, EVENT ON `app\\_prod`.* TO `backup_ro`@`%`",
		}, nil},
		{"global privileges", []string{
			"GRANT SELECT, SHOW VIEW, TRIGGER, LOCK TABLES, EVENT ON *.* TO `backup_ro`@`%`",
		}, nil},
		{"all privileges", []string{"GRANT ALL PRIVILEGES ON `app_prod`.* TO `u`@`%`"}, nil},
		{"some privileges missing", []string{
			"GRANT SELECT, LOCK TABLES ON `app_prod`.* TO `backup_ro`@`%`",
		}, []string{"SHOW VIEW", "TRIGGER", "EVENT"}},
		{"grant on a different database", []string{
			"GRANT SELECT, SHOW VIEW, TRIGGER, LOCK TABLES, EVENT ON `other`.* TO `backup_ro`@`%`",
		}, RequiredPrivileges},
		{"table grant only", []string{
			"GRANT SELECT ON `app_prod`.`customers` TO `backup_ro`@`%`",
		}, RequiredPrivileges},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MissingPrivileges(c.grants, "app_prod")
			if !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseClientVersionMySQL(t *testing.T) {
	cases := []struct {
		text    string
		major   int
		mariaDB bool
	}{
		{"mysqldump  Ver 8.0.36 for Linux on x86_64 (MySQL Community Server - GPL)", 8, false},
		{"mysqldump  Ver 10.19 Distrib 10.11.6-MariaDB, for debian-linux-gnu (x86_64)", 10, true},
		{"mariadb-dump from 11.4.2-MariaDB, client 10.19 for Linux (x86_64)", 11, true},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			info := ParseClientVersion(c.text)
			if info.Major != c.major || info.MariaDB != c.mariaDB {
				t.Fatalf("got %+v", info)
			}
		})
	}
}

func TestSplitAccount(t *testing.T) {
	user, host := splitAccount("backup_ro@10.0.0.%")
	if user != "backup_ro" || host != "10.0.0.%" {
		t.Fatalf("got %s %s", user, host)
	}
}

func TestSchemaCheckArgs(t *testing.T) {
	m := testEngine(t)
	args := m.dumpArgs("/tmp/x.cnf", "app_prod", false, "--no-data")
	if !slices.Contains(args, "--no-data") || strings.Join(args[len(args)-2:], " ") != "--databases app_prod" {
		t.Fatalf("got %v", args)
	}
}

var _ engine.Full = (*MySQL)(nil)
