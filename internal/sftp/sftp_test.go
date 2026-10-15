package sftp

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type testServer struct {
	host    string
	port    int
	hostKey ssh.Signer
}

func newSigner(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer, private
}

func startServer(t *testing.T, clientKey ssh.PublicKey) *testServer {
	t.Helper()
	hostKey, _ := newSigner(t)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(clientKey.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unknown key")
		},
	}
	cfg.AddHostKey(hostKey)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveConn(conn, cfg)
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return &testServer{host: "127.0.0.1", port: addr.Port, hostKey: hostKey}
}

func serveConn(conn net.Conn, cfg *ssh.ServerConfig) {
	_, channels, requests, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		conn.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range channelRequests {
				ok := req.Type == "subsystem" && string(req.Payload[4:]) == "sftp"
				req.Reply(ok, nil)
				if ok {
					server, err := sftp.NewServer(channel)
					if err == nil {
						server.Serve()
					}
					channel.Close()
				}
			}
		}()
	}
}

type fixture struct {
	server *testServer
	cfg    Config
	root   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	clientSigner, clientPrivate := newSigner(t)
	server := startServer(t, clientSigner.PublicKey())
	block, err := ssh.MarshalPrivateKey(clientPrivate, "dbak@test")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	hostKey, err := FetchHostKey(server.host, server.port, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(KnownHostsLine(server.host, server.port, hostKey)), 0o600); err != nil {
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
			Host:           server.host,
			Port:           server.port,
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
	other, _ := newSigner(t)
	line := KnownHostsLine(f.server.host, f.server.port, other.PublicKey())
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
	_, other := newSigner(t)
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
	signer, _ := newSigner(t)
	if !strings.HasPrefix(Fingerprint(signer.PublicKey()), "SHA256:") {
		t.Fatal("the fingerprint does not start with SHA256:")
	}
}
