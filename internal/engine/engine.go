package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	FlavorPostgres = "postgres"
	FlavorMySQL    = "mysql"
	FlavorMariaDB  = "mariadb"
)

type Options struct {
	Host     string
	Port     int
	Username string
	Password string
	SSLMode  string
	TempDir  string
	Binary   string
}

type ServerInfo struct {
	Flavor      string
	Version     string
	Major       int
	GTIDEnabled bool
}

type Engine interface {
	Name() string
	FileExtension() string
	Probe(ctx context.Context, database string) (ServerInfo, error)
	DumpCommand(ctx context.Context, database string) (*Dump, error)
	WrapOutput(w io.Writer) (io.WriteCloser, error)
}

type Dump struct {
	Cmd     *exec.Cmd
	cleanup func() error
}

func NewDump(cmd *exec.Cmd, cleanup func() error) *Dump {
	return &Dump{Cmd: cmd, cleanup: cleanup}
}

func (d *Dump) Cleanup() error {
	if d == nil || d.cleanup == nil {
		return nil
	}
	err := d.cleanup()
	d.cleanup = nil
	return err
}

type ConnectError struct {
	Message string
	Err     error
}

func (e *ConnectError) Error() string {
	return e.Message
}

func (e *ConnectError) Unwrap() error {
	return e.Err
}

func WriteSecretFile(dir, pattern, content string) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func RemoveFile(name string) func() error {
	return func() error {
		err := os.Remove(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
}

func EnvWithout(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, name := range names {
			if key == name {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, entry)
		}
	}
	return out
}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error {
	return nil
}

func NopWriteCloser(w io.Writer) io.WriteCloser {
	return nopCloser{w}
}

const tailLimit = 4096

type TailBuffer struct {
	data []byte
}

func NewTailBuffer() *TailBuffer {
	return &TailBuffer{}
}

func (t *TailBuffer) Write(p []byte) (int, error) {
	t.data = append(t.data, p...)
	if len(t.data) > tailLimit {
		t.data = t.data[len(t.data)-tailLimit:]
	}
	return len(p), nil
}

func (t *TailBuffer) LastLine() string {
	lines := strings.Split(strings.TrimSpace(string(t.data)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func CommandError(tool string, waitErr error, lastLine string) error {
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) && exitErr.ExitCode() >= 0 {
		if lastLine == "" {
			return fmt.Errorf("%s stopped with exit code %d. Examine the database server log", tool, exitErr.ExitCode())
		}
		return fmt.Errorf("%s stopped with exit code %d: %s", tool, exitErr.ExitCode(), lastLine)
	}
	return fmt.Errorf("%s stopped: %w", tool, waitErr)
}

func RunCheck(cmd *exec.Cmd) error {
	stderr := NewTailBuffer()
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	tool := filepath.Base(cmd.Path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start %s: %w. Make sure that the client tool is installed", tool, err)
	}
	if err := cmd.Wait(); err != nil {
		return CommandError(tool, err, stderr.LastLine())
	}
	return nil
}

type Inspector interface {
	ProbeServer(ctx context.Context) (ServerInfo, error)
	ListDatabases(ctx context.Context) ([]string, error)
	ClientVersion(ctx context.Context) (ClientInfo, error)
	SchemaCheck(ctx context.Context, database string) error
	PrivilegeCheck(ctx context.Context, database string) (PrivilegeReport, error)
}

type Full interface {
	Engine
	Inspector
}

type ClientInfo struct {
	Text    string
	Major   int
	MariaDB bool
}

type PrivilegeReport struct {
	Problems []string
	FixSQL   string
}

func (r PrivilegeReport) OK() bool {
	return len(r.Problems) == 0
}

type ClientTooOldError struct {
	Tool   string
	Client int
	Server int
}

func (e *ClientTooOldError) Error() string {
	return fmt.Sprintf("%s version %d is older than the server version %d. Install the client tools for version %d", e.Tool, e.Client, e.Server, e.Server)
}

func ToolOutput(ctx context.Context, binary string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, binary, args...).Output()
	if err != nil {
		return "", fmt.Errorf("cannot run %s: %w. Make sure that the client tool is installed", binary, err)
	}
	return strings.TrimSpace(string(out)), nil
}
