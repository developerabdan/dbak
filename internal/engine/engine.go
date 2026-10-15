package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
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
