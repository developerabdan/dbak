package sftp

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const DefaultTimeout = 30 * time.Second

var (
	ErrHostKeyChanged = errors.New("the SFTP host key changed. Do not continue until you know the cause")
	ErrHostKeyUnknown = errors.New("the SFTP host key is not in the known_hosts file. Run \"dbak init\" to pin the host key")
	ErrAuthRejected   = errors.New("the SFTP server rejected the key. Make sure that the public key is in ~/.ssh/authorized_keys on the server")
)

type Config struct {
	Host           string
	Port           int
	Username       string
	PrivateKeyFile string
	KnownHostsFile string
	Timeout        time.Duration
}

func (c Config) Address() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

type Client struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}

func Dial(cfg Config) (*Client, error) {
	signer, err := loadSigner(cfg.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	hostKeyCallback, hostKeyErr, algorithms, err := pinnedHostKeyCallback(cfg.KnownHostsFile, cfg.Address())
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	sshClient, err := ssh.Dial("tcp", cfg.Address(), &ssh.ClientConfig{
		User:              cfg.Username,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: algorithms,
		Timeout:           timeout,
	})
	if err != nil {
		if *hostKeyErr != nil {
			return nil, *hostKeyErr
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, ErrAuthRejected
		}
		return nil, fmt.Errorf("cannot connect to the SFTP server %s: %w. Make sure that the host is correct and the port is open", cfg.Address(), err)
	}
	sftpClient, err := sftp.NewClient(sshClient, sftp.UseConcurrentWrites(true))
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("the SFTP subsystem on %s did not start: %w. Make sure that the server allows SFTP", cfg.Address(), err)
	}
	return &Client{ssh: sshClient, sftp: sftpClient}, nil
}

func loadSigner(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the private key file %s: %w. Make sure that the file exists and the dbak user can read it", path, err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		var passErr *ssh.PassphraseMissingError
		if errors.As(err, &passErr) {
			return nil, fmt.Errorf("the private key %s has a passphrase. Use a key without a passphrase", path)
		}
		return nil, fmt.Errorf("the private key %s is not a valid SSH private key. Make a new key with \"dbak init\"", path)
	}
	return signer, nil
}

func pinnedHostKeyCallback(knownHostsFile, address string) (ssh.HostKeyCallback, *error, []string, error) {
	check, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot read the known_hosts file %s: %w. Run \"dbak init\" to pin the host key", knownHostsFile, err)
	}
	var classified error
	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) > 0 {
			classified = ErrHostKeyChanged
		} else if errors.As(err, &keyErr) {
			classified = ErrHostKeyUnknown
		} else {
			classified = err
		}
		return classified
	}
	return callback, &classified, pinnedAlgorithms(check, address), nil
}

func pinnedAlgorithms(check ssh.HostKeyCallback, address string) []string {
	probe, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		return nil
	}
	var keyErr *knownhosts.KeyError
	if !errors.As(check(address, &net.TCPAddr{IP: net.IPv6unspecified}, probe), &keyErr) {
		return nil
	}
	var algorithms []string
	for _, known := range keyErr.Want {
		for _, algorithm := range algorithmsForKeyType(known.Key.Type()) {
			if !slices.Contains(algorithms, algorithm) {
				algorithms = append(algorithms, algorithm)
			}
		}
	}
	return algorithms
}

func algorithmsForKeyType(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{keyType}
}

func (c *Client) MkdirAll(dir string) error {
	if err := c.sftp.MkdirAll(dir); err != nil {
		return fmt.Errorf("cannot make the directory %s: %w. Make sure that the SFTP user has write permission", dir, err)
	}
	return nil
}

func (c *Client) Create(path string) (io.WriteCloser, error) {
	file, err := c.sftp.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return nil, fmt.Errorf("cannot write to %s: %w. Make sure that the SFTP user has write permission", path, err)
	}
	return file, nil
}

func (c *Client) Open(path string) (io.ReadCloser, error) {
	file, err := c.sftp.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	return file, nil
}

func (c *Client) ReadDir(dir string) ([]os.FileInfo, error) {
	entries, err := c.sftp.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot read the directory %s: %w", dir, err)
	}
	return entries, nil
}

func (c *Client) Rename(oldPath, newPath string) error {
	if _, ok := c.sftp.HasExtension("posix-rename@openssh.com"); ok {
		if err := c.sftp.PosixRename(oldPath, newPath); err != nil {
			return fmt.Errorf("cannot rename %s to %s: %w", oldPath, newPath, err)
		}
		return nil
	}
	if err := c.sftp.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("cannot rename %s to %s: %w", oldPath, newPath, err)
	}
	return nil
}

func (c *Client) Remove(path string) error {
	if err := c.sftp.Remove(path); err != nil {
		return fmt.Errorf("cannot remove %s: %w", path, err)
	}
	return nil
}

func (c *Client) Close() error {
	return errors.Join(c.sftp.Close(), c.ssh.Close())
}

func FetchHostKey(host string, port int, timeout time.Duration) (ssh.PublicKey, error) {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	var hostKey ssh.PublicKey
	errStop := errors.New("host key received")
	_, err := ssh.Dial("tcp", address, &ssh.ClientConfig{
		User: "dbak",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			hostKey = key
			return errStop
		},
		Timeout: timeout,
	})
	if hostKey != nil {
		return hostKey, nil
	}
	return nil, fmt.Errorf("cannot get the host key from %s: %w. Make sure that the host is correct and the port is open", address, err)
}

func KnownHostsLine(host string, port int, key ssh.PublicKey) string {
	address := knownhosts.Normalize(net.JoinHostPort(host, strconv.Itoa(port)))
	return knownhosts.Line([]string{address}, key) + "\n"
}

func Fingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}
