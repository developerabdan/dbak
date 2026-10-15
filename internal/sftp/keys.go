package sftp

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"golang.org/x/crypto/ssh"
)

func GenerateKey(comment string) (privatePEM []byte, authorizedKey string, err error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("cannot make an ed25519 key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(private, comment)
	if err != nil {
		return nil, "", fmt.Errorf("cannot encode the private key: %w", err)
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return nil, "", fmt.Errorf("cannot encode the public key: %w", err)
	}
	return pem.EncodeToMemory(block), authorizedLine(sshPublic, comment), nil
}

func PublicKeyFromFile(privateKeyFile, comment string) (string, error) {
	signer, err := loadSigner(privateKeyFile)
	if err != nil {
		return "", err
	}
	return authorizedLine(signer.PublicKey(), comment), nil
}

func authorizedLine(key ssh.PublicKey, comment string) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	if comment != "" {
		line += " " + comment
	}
	return line
}

func AuthorizeCommand(authorizedKey string) string {
	return fmt.Sprintf("mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '%s' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys", authorizedKey)
}

func (c *Client) WriteCheck(dir string) error {
	if err := c.MkdirAll(dir); err != nil {
		return err
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return fmt.Errorf("cannot make random test data: %w", err)
	}
	name := path.Join(dir, ".dbak-write-test-"+hex.EncodeToString(token[:4]))
	content := []byte("dbak write test " + hex.EncodeToString(token) + "\n")
	w, err := c.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(content); err != nil {
		w.Close()
		c.Remove(name)
		return fmt.Errorf("cannot write to %s: %w. Make sure that the SFTP user has write permission", name, err)
	}
	if err := w.Close(); err != nil {
		c.Remove(name)
		return fmt.Errorf("cannot write to %s: %w. Make sure that the server has free space", name, err)
	}
	r, err := c.Open(name)
	if err != nil {
		c.Remove(name)
		return err
	}
	readBack, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		c.Remove(name)
		return fmt.Errorf("cannot read back %s: %w", name, err)
	}
	if !bytes.Equal(readBack, content) {
		c.Remove(name)
		return fmt.Errorf("the data in %s is different after the upload. Examine the disk of the SFTP server", name)
	}
	return c.Remove(name)
}

func WriteKnownHosts(path, host string, port int, key ssh.PublicKey) error {
	if err := os.WriteFile(path, []byte(KnownHostsLine(host, port, key)), 0o600); err != nil {
		return fmt.Errorf("cannot write the known_hosts file %s: %w", path, err)
	}
	return nil
}
