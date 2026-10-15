package engine

import (
	"os/exec"
	"strings"
	"testing"
)

func TestTailBufferKeepsLastLine(t *testing.T) {
	buf := NewTailBuffer()
	buf.Write([]byte(strings.Repeat("x", 5000) + "\nfirst\n"))
	buf.Write([]byte("last line\n\n"))
	if got := buf.LastLine(); got != "last line" {
		t.Fatalf("got %q", got)
	}
	if len(buf.data) > tailLimit {
		t.Fatalf("got %d bytes, want %d or less", len(buf.data), tailLimit)
	}
}

func TestRunCheck(t *testing.T) {
	if err := RunCheck(exec.Command("/bin/sh", "-c", "echo ok")); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	err := RunCheck(exec.Command("/bin/sh", "-c", "echo 'pg_dump: error: denied' >&2; exit 1"))
	if err == nil || !strings.Contains(err.Error(), "sh stopped with exit code 1: pg_dump: error: denied") {
		t.Fatalf("got %v", err)
	}
	err = RunCheck(exec.Command("/nonexistent/tool"))
	if err == nil || !strings.Contains(err.Error(), "client tool is installed") {
		t.Fatalf("got %v", err)
	}
}

func TestEnvWithout(t *testing.T) {
	got := EnvWithout([]string{"A=1", "PGPASSWORD=x", "B=2"}, "PGPASSWORD")
	if strings.Join(got, ",") != "A=1,B=2" {
		t.Fatalf("got %v", got)
	}
}
