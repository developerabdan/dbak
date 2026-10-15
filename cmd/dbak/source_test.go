package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/developerabdan/dbak/internal/config"
)

func TestWriteSources(t *testing.T) {
	cfg := &config.Config{Sources: []config.Source{
		{Name: "main_pg", Engine: config.EnginePostgres, Host: "127.0.0.1", Port: 5432, Username: "backup_ro", Databases: []string{"app_prod", "app_audit"}},
		{Name: "shop", Engine: config.EngineMySQL, Host: "127.0.0.1", Port: 3307, Username: "backup_ro", Databases: []string{"shop"}, Retention: &config.Retention{Daily: 14}},
	}}
	var out bytes.Buffer
	if err := writeSources(&out, cfg); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"NAME     ENGINE    SERVER          USER       DATABASES            RETENTION",
		"main_pg  postgres  127.0.0.1:5432  backup_ro  app_prod, app_audit  global",
		"shop     mysql     127.0.0.1:3307  backup_ro  shop                 14 daily, 0 weekly, 0 monthly",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output does not contain %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "password") {
		t.Fatal("the output names a password")
	}
}
