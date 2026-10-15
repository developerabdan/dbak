package lock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSecondAcquireFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbak.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("got %v, want ErrLocked", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(path)
	if err != nil {
		t.Fatalf("got %v after release, want no error", err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireInMissingDirectoryFails(t *testing.T) {
	if _, err := Acquire(filepath.Join(t.TempDir(), "none", "dbak.lock")); err == nil {
		t.Fatal("got no error, want an error")
	}
}
