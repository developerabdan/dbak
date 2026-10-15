package sftptest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Server struct {
	Host    string
	Port    int
	HostKey ssh.Signer
	mu      sync.Mutex
	allowed [][]byte
}

func NewSigner(t testing.TB) (ssh.Signer, ed25519.PrivateKey) {
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

func WritePrivateKey(t testing.TB, dir string, private ed25519.PrivateKey) string {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(private, "dbak@test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func Start(t testing.TB) *Server {
	t.Helper()
	hostKey, _ := NewSigner(t)
	s := &Server{Host: "127.0.0.1", HostKey: hostKey}
	cfg := &ssh.ServerConfig{PublicKeyCallback: s.checkKey}
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
	s.Port = listener.Addr().(*net.TCPAddr).Port
	return s
}

func (s *Server) Allow(key ssh.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowed = append(s.allowed, key.Marshal())
}

func (s *Server) checkKey(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, allowed := range s.allowed {
		if string(allowed) == string(key.Marshal()) {
			return nil, nil
		}
	}
	return nil, errors.New("unknown key")
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
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
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
