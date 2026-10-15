package sftp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"dbak/internal/sftp/sftptest"
)

type fixture struct {
	server *sftptest.Server
	cfg    Config
	root   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	clientSigner, clientPrivate := sftptest.NewSigner(t)
	server := sftptest.Start(t)
	server.Allow(clientSigner.PublicKey())
	keyFile := sftptest.WritePrivateKey(t, dir, clientPrivate)
	hostKey, err := FetchHostKey(server.Host, server.Port, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(KnownHostsLine(server.Host, server.Port, hostKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "remote")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &fixture{
		server: server,
		root:   root,
		cfg: Config{
			Host:           server.Host,
			Port:           server.Port,
			Username:       "dbak",
			PrivateKeyFile: keyFile,
			KnownHostsFile: knownHosts,
			Timeout:        5 * time.Second,
		},
	}
}

func TestUploadRenameAndRemove(t *testing.T) {
	f := newFixture(t)
	client, err := Dial(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	dir := filepath.Join(f.root, "vm-app-01", "app_prod")
	if err := client.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(dir, "app_prod_20261015T190000Z.dump.partial")
	final := strings.TrimSuffix(partial, ".partial")
	w, err := client.Create(partial)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("backup data ", 100000)
	if _, err := io.WriteString(w, payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Rename(partial, final); err != nil {
		t.Fatal(err)
	}
	entries, err := client.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(final) {
		t.Fatalf("got %v %v, want only the final file", entries, err)
	}
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatal("the uploaded data is different")
	}
	if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the partial file exists after rename")
	}
	r, err := client.Open(final)
	if err != nil {
		t.Fatal(err)
	}
	readBack, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(readBack) != payload {
		t.Fatalf("read back failed: %v", err)
	}
	if err := client.Remove(final); err != nil {
		t.Fatal(err)
	}
}

func TestChangedHostKeyIsRejected(t *testing.T) {
	f := newFixture(t)
	other, _ := sftptest.NewSigner(t)
	line := KnownHostsLine(f.server.Host, f.server.Port, other.PublicKey())
	if err := os.WriteFile(f.cfg.KnownHostsFile, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(f.cfg); !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("got %v, want ErrHostKeyChanged", err)
	}
}

func TestUnknownHostKeyIsRejected(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.cfg.KnownHostsFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(f.cfg); !errors.Is(err, ErrHostKeyUnknown) {
		t.Fatalf("got %v, want ErrHostKeyUnknown", err)
	}
}

func TestWrongClientKeyIsRejected(t *testing.T) {
	f := newFixture(t)
	_, other := sftptest.NewSigner(t)
	block, err := ssh.MarshalPrivateKey(other, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.cfg.PrivateKeyFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(f.cfg); !errors.Is(err, ErrAuthRejected) {
		t.Fatalf("got %v, want ErrAuthRejected", err)
	}
}

func TestFingerprintFormat(t *testing.T) {
	signer, _ := sftptest.NewSigner(t)
	if !strings.HasPrefix(Fingerprint(signer.PublicKey()), "SHA256:") {
		t.Fatal("the fingerprint does not start with SHA256:")
	}
}

func TestWriteCheck(t *testing.T) {
	f := newFixture(t)
	client, err := Dial(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	dir := filepath.Join(f.root, "vm-app-01")
	if err := client.WriteCheck(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("got %v %v, want an empty directory", entries, err)
	}
}

func TestGenerateKeyWorksForLogin(t *testing.T) {
	f := newFixture(t)
	privatePEM, authorized, err := GenerateKey("dbak@vm-app-01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(authorized, "ssh-ed25519 ") || !strings.HasSuffix(authorized, " dbak@vm-app-01") {
		t.Fatalf("got %q", authorized)
	}
	if err := os.WriteFile(f.cfg.PrivateKeyFile, privatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(f.cfg); !errors.Is(err, ErrAuthRejected) {
		t.Fatalf("got %v before the key is allowed, want ErrAuthRejected", err)
	}
	public, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorized))
	if err != nil {
		t.Fatal(err)
	}
	f.server.Allow(public)
	client, err := Dial(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	fromFile, err := PublicKeyFromFile(f.cfg.PrivateKeyFile, "dbak@vm-app-01")
	if err != nil || fromFile != authorized {
		t.Fatalf("got %q %v, want %q", fromFile, err, authorized)
	}
	if !strings.Contains(AuthorizeCommand(authorized), "authorized_keys") {
		t.Fatal("the command does not name authorized_keys")
	}
}

func TestWriteKnownHosts(t *testing.T) {
	f := newFixture(t)
	key, err := FetchHostKey(f.server.Host, f.server.Port, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(key) != Fingerprint(f.server.HostKey.PublicKey()) {
		t.Fatal("the fetched host key is different")
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := WriteKnownHosts(path, f.server.Host, f.server.Port, key); err != nil {
		t.Fatal(err)
	}
	f.cfg.KnownHostsFile = path
	client, err := Dial(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
}

func TestDialUsesThePinnedKeyType(t *testing.T) {
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaSigner, err := ssh.NewSignerFromKey(ecdsaKey)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	clientSigner, clientPrivate := sftptest.NewSigner(t)
	server := sftptest.StartWithHostKeys(t, ecdsaSigner)
	server.Allow(clientSigner.PublicKey())
	f.cfg.Host, f.cfg.Port = server.Host, server.Port
	f.cfg.PrivateKeyFile = sftptest.WritePrivateKey(t, t.TempDir(), clientPrivate)
	for name, key := range map[string]ssh.PublicKey{"ed25519": server.HostKey.PublicKey(), "ecdsa": ecdsaSigner.PublicKey()} {
		t.Run("only the "+name+" key is pinned", func(t *testing.T) {
			line := KnownHostsLine(server.Host, server.Port, key)
			if err := os.WriteFile(f.cfg.KnownHostsFile, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			client, err := Dial(f.cfg)
			if err != nil {
				t.Fatalf("got %v", err)
			}
			client.Close()
		})
	}
}
