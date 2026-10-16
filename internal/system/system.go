package system

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	FamilyDebian = "debian"
	FamilyRHEL   = "rhel"
)

const ServiceUser = "dbak"

type OSRelease struct {
	ID        string
	IDLike    []string
	VersionID string
}

func IsRoot() bool {
	return os.Geteuid() == 0
}

func HasSystemd() bool {
	info, err := os.Stat("/run/systemd/system")
	return err == nil && info.IsDir()
}

func ReadOSRelease(path string) (OSRelease, error) {
	file, err := os.Open(path)
	if err != nil {
		return OSRelease{}, err
	}
	defer file.Close()
	var rel OSRelease
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "ID":
			rel.ID = value
		case "ID_LIKE":
			rel.IDLike = strings.Fields(value)
		case "VERSION_ID":
			rel.VersionID = value
		}
	}
	return rel, scanner.Err()
}

func (r OSRelease) Family() string {
	for _, id := range append([]string{r.ID}, r.IDLike...) {
		switch id {
		case "debian", "ubuntu":
			return FamilyDebian
		case "rhel", "centos", "fedora", "rocky", "almalinux", "ol", "amzn":
			return FamilyRHEL
		}
	}
	return ""
}

func ClientPackage(family, engine string) string {
	switch {
	case family == FamilyDebian && engine == "postgres":
		return "postgresql-client"
	case family == FamilyDebian && engine == "mysql":
		return "default-mysql-client"
	case family == FamilyRHEL && engine == "postgres":
		return "postgresql"
	case family == FamilyRHEL && engine == "mysql":
		return "mysql"
	}
	return ""
}

func InstallCommands(family, pkg string) [][]string {
	switch family {
	case FamilyDebian:
		return [][]string{{"apt-get", "update"}, {"apt-get", "install", "-y", pkg}}
	case FamilyRHEL:
		if _, err := exec.LookPath("dnf"); err == nil {
			return [][]string{{"dnf", "install", "-y", pkg}}
		}
		return [][]string{{"yum", "install", "-y", pkg}}
	}
	return nil
}

func PGDGInstructions(family string, major int) []string {
	switch family {
	case FamilyDebian:
		return []string{
			"apt-get install -y postgresql-common",
			"/usr/share/postgresql-common/pgdg/apt.postgresql.org.sh -y",
			fmt.Sprintf("apt-get install -y postgresql-client-%d", major),
		}
	case FamilyRHEL:
		return []string{
			"dnf install -y https://download.postgresql.org/pub/repos/yum/reporpms/EL-$(rpm -E %rhel)-$(uname -m)/pgdg-redhat-repo-latest.noarch.rpm",
			fmt.Sprintf("dnf install -y postgresql%d", major),
		}
	}
	return []string{fmt.Sprintf("Install the PostgreSQL %d client tools from https://www.postgresql.org/download/", major)}
}

func DetectTimezone() string {
	return detectTimezone("/etc/timezone", "/etc/localtime", os.Getenv("TZ"))
}

func detectTimezone(timezoneFile, localtimeLink, tzEnv string) string {
	if data, err := os.ReadFile(timezoneFile); err == nil {
		if name := strings.TrimSpace(string(data)); validZone(name) {
			return name
		}
	}
	if target, err := filepath.EvalSymlinks(localtimeLink); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok && validZone(name) {
			return name
		}
	}
	if name := strings.TrimPrefix(tzEnv, ":"); validZone(name) {
		return name
	}
	return "UTC"
}

func validZone(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func PrimaryIP() string {
	conn, err := net.Dial("udp", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

func Hostname() (string, error) {
	name, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("cannot get the host name: %w", err)
	}
	return name, nil
}

type Owner struct {
	UID int
	GID int
}

func EnsureServiceUser() (Owner, error) {
	owner, err := LookupOwner(ServiceUser)
	if err == nil {
		return owner, nil
	}
	var unknown user.UnknownUserError
	if !errors.As(err, &unknown) {
		return Owner{}, err
	}
	cmd := exec.Command("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", ServiceUser)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Owner{}, fmt.Errorf("cannot make the system user %s: %s. Make the user manually", ServiceUser, strings.TrimSpace(string(out)))
	}
	return LookupOwner(ServiceUser)
}

func LookupOwner(name string) (Owner, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return Owner{}, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return Owner{}, fmt.Errorf("the user %s has a UID that is not a number: %s", name, u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return Owner{}, fmt.Errorf("the user %s has a GID that is not a number: %s", name, u.Gid)
	}
	return Owner{UID: uid, GID: gid}, nil
}

func CurrentOwner() Owner {
	return Owner{UID: os.Getuid(), GID: os.Getgid()}
}

func (o Owner) Apply(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("cannot set the mode of %s: %w", path, err)
	}
	if o.UID == os.Getuid() && o.GID == os.Getgid() {
		return nil
	}
	if err := os.Chown(path, o.UID, o.GID); err != nil {
		return fmt.Errorf("cannot set the owner of %s: %w", path, err)
	}
	return nil
}

func (o Owner) MkdirAll(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("cannot make the directory %s: %w", path, err)
	}
	return o.Apply(path, mode)
}

func ProcessRunning(names ...string) bool {
	return processRunning("/proc", names...)
}

func processRunning(procDir string, names ...string) bool {
	matches, _ := filepath.Glob(filepath.Join(procDir, "[0-9]*", "comm"))
	for _, file := range matches {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(data))
		for _, want := range names {
			if name == want {
				return true
			}
		}
	}
	return false
}

func InvokingUser() (string, Owner, error) {
	return invokingUser(os.Getenv("SUDO_USER"), user.Lookup, os.UserHomeDir)
}

func invokingUser(sudoUser string, lookup func(string) (*user.User, error), homeDir func() (string, error)) (string, Owner, error) {
	if sudoUser == "" || sudoUser == "root" {
		home, err := homeDir()
		if err != nil {
			return "", Owner{}, fmt.Errorf("cannot find the home directory: %w", err)
		}
		return home, CurrentOwner(), nil
	}
	u, err := lookup(sudoUser)
	if err != nil {
		return "", Owner{}, fmt.Errorf("cannot find the user %s: %w", sudoUser, err)
	}
	uid, uidErr := strconv.Atoi(u.Uid)
	gid, gidErr := strconv.Atoi(u.Gid)
	if uidErr != nil || gidErr != nil {
		return "", Owner{}, fmt.Errorf("the user %s has a UID or a GID that is not a number", sudoUser)
	}
	return u.HomeDir, Owner{UID: uid, GID: gid}, nil
}
