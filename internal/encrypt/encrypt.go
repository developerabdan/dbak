package encrypt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"
)

const Extension = "age"

const hybridPrefix = "age1pq1"

var ErrKeyMismatch = errors.New("the private key does not match the public key. Paste the key that dbak showed")

type KeyPair struct {
	Private string
	Public  string
}

func ParseRecipient(value string) (*age.X25519Recipient, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, hybridPrefix) {
		return nil, errors.New("dbak does not accept post-quantum age keys. Use a key from age-keygen without the -pq option")
	}
	recipient, err := age.ParseX25519Recipient(value)
	if err != nil {
		return nil, errors.New("the value is not a valid age public key. Use a key that starts with age1")
	}
	return recipient, nil
}

func ParseRecipients(values []string) ([]age.Recipient, error) {
	recipients := make([]age.Recipient, 0, len(values))
	for _, value := range values {
		recipient, err := ParseRecipient(value)
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, recipient)
	}
	return recipients, nil
}

func NewWriter(w io.Writer, recipients []age.Recipient) (io.WriteCloser, error) {
	if len(recipients) == 0 {
		return nil, errors.New("the encryption has no public key. Add a public key with dbak init")
	}
	out, err := age.Encrypt(w, recipients...)
	if err != nil {
		return nil, fmt.Errorf("cannot start the encryption: %w", err)
	}
	return out, nil
}

func GenerateKey() (KeyPair, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return KeyPair{}, fmt.Errorf("cannot make a key: %w", err)
	}
	return KeyPair{Private: identity.String(), Public: identity.Recipient().String()}, nil
}

func CheckPrivateKey(private, public string) error {
	identity, err := age.ParseX25519Identity(strings.TrimSpace(private))
	if err != nil {
		return errors.New("the value is not a valid age private key. Paste the full key that starts with AGE-SECRET-KEY-1")
	}
	if identity.Recipient().String() != strings.TrimSpace(public) {
		return ErrKeyMismatch
	}
	return roundTrip(identity)
}

func roundTrip(identity *age.X25519Identity) error {
	const text = "dbak encryption test"
	var sealed bytes.Buffer
	w, err := age.Encrypt(&sealed, identity.Recipient())
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, text); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	r, err := age.Decrypt(&sealed, identity)
	if err != nil {
		return fmt.Errorf("the test decryption failed: %w", err)
	}
	opened, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("the test decryption failed: %w", err)
	}
	if string(opened) != text {
		return errors.New("the test decryption gave different text")
	}
	return nil
}
