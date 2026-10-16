package restore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/developerabdan/dbak/internal/encrypt"
	"github.com/developerabdan/dbak/internal/system"
)

type memoryStore map[string][]byte

func (m memoryStore) Open(path string) (io.ReadCloser, error) {
	data, ok := m[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func compress(t *testing.T, text string) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := zstd.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, text); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func seal(t *testing.T, public string, data []byte) []byte {
	t.Helper()
	recipients, err := encrypt.ParseRecipients([]string{public})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w, err := encrypt.NewWriter(&out, recipients)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestOutputName(t *testing.T) {
	cases := map[string]string{
		"app_prod_20261015T190000Z.dump":         "app_prod_20261015T190000Z.dump",
		"app_prod_20261015T190000Z.dump.age":     "app_prod_20261015T190000Z.dump",
		"shop_20261015T190000Z.sql.zst":          "shop_20261015T190000Z.sql",
		"shop_20261015T190000Z.sql.zst.age":      "shop_20261015T190000Z.sql",
		"/backups/vm/main/shop/shop_x.sql.zst":   "/backups/vm/main/shop/shop_x.sql",
		"app_prod_20261015T190000Z.dump.partial": "app_prod_20261015T190000Z.dump.partial",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := OutputName(in); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestDownload(t *testing.T) {
	pair, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := encrypt.ParseIdentity(pair.Private)
	if err != nil {
		t.Fatal(err)
	}
	store := memoryStore{
		"/b/app_prod_20261015T190000Z.dump":     []byte("custom dump"),
		"/b/app_prod_20261015T190000Z.dump.age": seal(t, pair.Public, []byte("custom dump")),
		"/b/shop_20261015T190000Z.sql.zst":      compress(t, "CREATE DATABASE shop;"),
		"/b/shop_20261015T190000Z.sql.zst.age":  seal(t, pair.Public, compress(t, "CREATE DATABASE shop;")),
	}
	cases := []struct {
		name, remote, file, content string
	}{
		{"a PostgreSQL dump stays the same", "/b/app_prod_20261015T190000Z.dump", "app_prod_20261015T190000Z.dump", "custom dump"},
		{"an encrypted PostgreSQL dump is decrypted", "/b/app_prod_20261015T190000Z.dump.age", "app_prod_20261015T190000Z.dump", "custom dump"},
		{"a MySQL dump is decompressed", "/b/shop_20261015T190000Z.sql.zst", "shop_20261015T190000Z.sql", "CREATE DATABASE shop;"},
		{"an encrypted MySQL dump is decrypted and decompressed", "/b/shop_20261015T190000Z.sql.zst.age", "shop_20261015T190000Z.sql", "CREATE DATABASE shop;"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "backups")
			res, err := Download(context.Background(), store, Request{RemoteFile: c.remote, Dir: dir, Identity: identity, Owner: system.CurrentOwner()})
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(dir, c.file)
			if res.Path != want || res.SizeBytes != int64(len(c.content)) {
				t.Fatalf("got %+v, want %s with %d bytes", res, want, len(c.content))
			}
			got, err := os.ReadFile(want)
			if err != nil || string(got) != c.content {
				t.Fatalf("got %q and %v", got, err)
			}
			assertMode(t, want, fileMode)
			assertMode(t, dir, dirMode|os.ModeDir)
			if _, err := os.Stat(want + partialSuffix); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the partial file stays: %v", err)
			}
		})
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != want {
		t.Fatalf("got mode %v for %s, want %v", info.Mode(), path, want)
	}
}

func TestDownloadFailures(t *testing.T) {
	pair, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := encrypt.ParseIdentity(other.Private)
	if err != nil {
		t.Fatal(err)
	}
	sealed := seal(t, pair.Public, []byte("custom dump"))
	store := memoryStore{
		"/b/a_20261015T190000Z.dump.age": sealed,
		"/b/a_20261015T190000Z.dump":     []byte("custom dump"),
		"/b/c_20261015T190000Z.dump.age": sealed[:len(sealed)-8],
		"/b/s_20261015T190000Z.sql.zst":  []byte("not zstd data"),
	}
	owner := system.CurrentOwner()

	t.Run("an encrypted backup needs a key", func(t *testing.T) {
		dir := t.TempDir()
		_, err := Download(context.Background(), store, Request{RemoteFile: "/b/a_20261015T190000Z.dump.age", Dir: dir, Owner: owner})
		if err == nil || !strings.Contains(err.Error(), "private key") {
			t.Fatalf("got %v", err)
		}
		assertEmpty(t, dir)
	})

	t.Run("a wrong key writes no file", func(t *testing.T) {
		dir := t.TempDir()
		_, err := Download(context.Background(), store, Request{RemoteFile: "/b/a_20261015T190000Z.dump.age", Dir: dir, Identity: wrong, Owner: owner})
		if !errors.Is(err, encrypt.ErrWrongKey) {
			t.Fatalf("got %v, want ErrWrongKey", err)
		}
		assertEmpty(t, dir)
	})

	t.Run("damaged data removes the partial file", func(t *testing.T) {
		dir := t.TempDir()
		identity, err := encrypt.ParseIdentity(pair.Private)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Download(context.Background(), store, Request{RemoteFile: "/b/c_20261015T190000Z.dump.age", Dir: dir, Identity: identity, Owner: owner}); err == nil {
			t.Fatal("got no error")
		}
		assertEmpty(t, dir)
	})

	t.Run("data that is not zstd removes the partial file", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := Download(context.Background(), store, Request{RemoteFile: "/b/s_20261015T190000Z.sql.zst", Dir: dir, Owner: owner}); err == nil {
			t.Fatal("got no error")
		}
		assertEmpty(t, dir)
	})

	t.Run("an existing file stays the same", func(t *testing.T) {
		dir := t.TempDir()
		existing := filepath.Join(dir, "a_20261015T190000Z.dump")
		if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Download(context.Background(), store, Request{RemoteFile: "/b/a_20261015T190000Z.dump", Dir: dir, Owner: owner})
		if err == nil || !strings.Contains(err.Error(), "exists") {
			t.Fatalf("got %v", err)
		}
		if got, _ := os.ReadFile(existing); string(got) != "old" {
			t.Fatalf("the file changed to %q", got)
		}
	})

	t.Run("a relative directory is not accepted", func(t *testing.T) {
		if err := CheckTarget("backups", "/b/a_20261015T190000Z.dump"); err == nil {
			t.Fatal("got no error")
		}
	})

	t.Run("a stopped context removes the partial file", func(t *testing.T) {
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Download(ctx, store, Request{RemoteFile: "/b/a_20261015T190000Z.dump", Dir: dir, Owner: owner})
		if err == nil || !strings.Contains(err.Error(), "stopped") {
			t.Fatalf("got %v", err)
		}
		assertEmpty(t, dir)
	})
}

func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		t.Fatalf("the directory has %d files, want none", len(entries))
	}
}
