package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/developerabdan/dbak/internal/engine"
)

func TestParseClientVersion(t *testing.T) {
	cases := map[string]int{
		"pg_dump (PostgreSQL) 16.2 (Ubuntu 16.2-1.pgdg22.04+1)": 16,
		"pg_dump (PostgreSQL) 17.0 (DBngin.app)":                17,
		"pg_dump (PostgreSQL) 9.6.24":                           9,
	}
	for text, want := range cases {
		t.Run(text, func(t *testing.T) {
			info, err := ParseClientVersion(text)
			if err != nil || info.Major != want {
				t.Fatalf("got %+v %v, want major %d", info, err, want)
			}
		})
	}
	if _, err := ParseClientVersion("unknown output"); err == nil {
		t.Fatal("got no error for unknown output")
	}
}

func TestCheckClient(t *testing.T) {
	server := engine.ServerInfo{Major: 16}
	if err := CheckClient(engine.ClientInfo{Major: 17}, server); err != nil {
		t.Fatalf("got %v for a newer client", err)
	}
	if err := CheckClient(engine.ClientInfo{Major: 16}, server); err != nil {
		t.Fatalf("got %v for the same version", err)
	}
	var tooOld *engine.ClientTooOldError
	if err := CheckClient(engine.ClientInfo{Major: 14}, server); !errors.As(err, &tooOld) || tooOld.Server != 16 {
		t.Fatalf("got %v, want a client too old error", err)
	}
}

func TestGrantFix(t *testing.T) {
	got := grantFix([]string{"public", "Billing"}, "backup_ro")
	want := "GRANT USAGE ON SCHEMA \"public\" TO \"backup_ro\";\n" +
		"GRANT SELECT ON ALL TABLES IN SCHEMA \"public\" TO \"backup_ro\";\n" +
		"GRANT SELECT ON ALL SEQUENCES IN SCHEMA \"public\" TO \"backup_ro\";\n" +
		"GRANT USAGE ON SCHEMA \"Billing\" TO \"backup_ro\";\n" +
		"GRANT SELECT ON ALL TABLES IN SCHEMA \"Billing\" TO \"backup_ro\";\n" +
		"GRANT SELECT ON ALL SEQUENCES IN SCHEMA \"Billing\" TO \"backup_ro\";\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSchemaCheckCommandArgs(t *testing.T) {
	p := New(testOptions(t))
	cmd, cleanup, err := p.command(context.Background(), "app_prod", "--schema-only")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !slices.Contains(cmd.Args, "--schema-only") || !slices.Contains(cmd.Args, "app_prod") {
		t.Fatalf("got %v", cmd.Args)
	}
}

var _ engine.Full = (*Postgres)(nil)
