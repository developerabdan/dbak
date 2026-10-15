package system

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOSReleaseFamily(t *testing.T) {
	cases := []struct {
		name    string
		content string
		family  string
	}{
		{"ubuntu", "NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\nVERSION_ID=\"24.04\"\n", FamilyDebian},
		{"debian", "ID=debian\nVERSION_ID=\"12\"\n", FamilyDebian},
		{"rocky", "ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\n", FamilyRHEL},
		{"amazon linux", "ID=\"amzn\"\nID_LIKE=\"centos rhel fedora\"\n", FamilyRHEL},
		{"alpine", "ID=alpine\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rel, err := ReadOSRelease(writeFile(t, "os-release", c.content))
			if err != nil {
				t.Fatal(err)
			}
			if got := rel.Family(); got != c.family {
				t.Fatalf("got %q, want %q", got, c.family)
			}
		})
	}
}

func TestClientPackage(t *testing.T) {
	if ClientPackage(FamilyDebian, "mysql") != "default-mysql-client" || ClientPackage(FamilyRHEL, "postgres") != "postgresql" {
		t.Fatal("unexpected package names")
	}
	if ClientPackage("", "postgres") != "" {
		t.Fatal("got a package for an unknown family")
	}
}

func TestInstallCommand(t *testing.T) {
	if got := InstallCommand(FamilyDebian, "postgresql-client"); !slices.Equal(got, []string{"apt-get", "install", "-y", "postgresql-client"}) {
		t.Fatalf("got %v", got)
	}
	if InstallCommand("", "x") != nil {
		t.Fatal("got a command for an unknown family")
	}
}

func TestPGDGInstructions(t *testing.T) {
	lines := PGDGInstructions(FamilyDebian, 16)
	if !strings.Contains(lines[len(lines)-1], "postgresql-client-16") {
		t.Fatalf("got %v", lines)
	}
	lines = PGDGInstructions(FamilyRHEL, 15)
	if !strings.Contains(lines[len(lines)-1], "postgresql15") {
		t.Fatalf("got %v", lines)
	}
}

func TestDetectTimezone(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if got := detectTimezone(writeFile(t, "timezone", "Asia/Jakarta\n"), missing, ""); got != "Asia/Jakarta" {
		t.Fatalf("got %s from the timezone file", got)
	}
	zoneDir := filepath.Join(t.TempDir(), "zoneinfo", "Europe")
	if err := os.MkdirAll(zoneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	zoneFile := filepath.Join(zoneDir, "Berlin")
	if err := os.WriteFile(zoneFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "localtime")
	if err := os.Symlink(zoneFile, link); err != nil {
		t.Fatal(err)
	}
	if got := detectTimezone(missing, link, ""); got != "Europe/Berlin" {
		t.Fatalf("got %s from the localtime link", got)
	}
	if got := detectTimezone(missing, missing, ":America/New_York"); got != "America/New_York" {
		t.Fatalf("got %s from TZ", got)
	}
	if got := detectTimezone(writeFile(t, "timezone", "Bad/Zone"), missing, ""); got != "UTC" {
		t.Fatalf("got %s, want UTC", got)
	}
}

func TestOwnerApplySetsMode(t *testing.T) {
	path := writeFile(t, "secret", "x")
	if err := CurrentOwner().Apply(path, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("got %v", info.Mode().Perm())
	}
	dir := filepath.Join(t.TempDir(), "a", "secrets")
	if err := CurrentOwner().MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("got %v %v", info, err)
	}
}
