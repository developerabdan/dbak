package onboard

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/encrypt"
)

const (
	keyGenerated = "generate"
	keyOwn       = "own"
	keyOff       = "off"
	keyAdd       = "add"
	keyRemove    = "remove"
	keyKeep      = "keep"
	keyRetry     = "retry"
	keyShow      = "show"
)

func (s *session) configureEncryption() error {
	ui := s.env.UI
	ui.Heading("Backup encryption")
	ui.Info("dbak can encrypt the backups with age. The SFTP server gets only encrypted files. To restore a backup, you need the private key.")
	if s.cfg.Encryption.Enabled() {
		return s.editEncryption()
	}
	choice, err := ui.Select("Encrypt the backups?", "", options(
		"Yes, make a new key for me", keyGenerated,
		"Yes, I have an age public key", keyOwn,
		"No", keyOff,
	), keyGenerated)
	if err != nil {
		return err
	}
	if choice == keyOff {
		ui.Warn("The backups will not be encrypted. A person with access to the SFTP server can read them.")
		return nil
	}
	if err := s.addKey(choice); err != nil {
		return err
	}
	return s.askMoreKeys()
}

func (s *session) editEncryption() error {
	ui := s.env.UI
	ui.Info(fmt.Sprintf("Encryption is on with %s.", keyCount(len(s.cfg.Encryption.Recipients))))
	opts := options("Keep the current public keys", keyKeep, "Add a public key", keyAdd)
	if len(s.cfg.Encryption.Recipients) > 1 {
		opts = append(opts, options("Remove a public key", keyRemove)...)
	}
	opts = append(opts, options("Turn off the encryption", keyOff)...)
	choice, err := ui.Select("What do you want to change?", "", opts, keyKeep)
	if err != nil {
		return err
	}
	switch choice {
	case keyAdd:
		return s.askNewKey()
	case keyRemove:
		return s.removeKey()
	case keyOff:
		return s.turnOffEncryption()
	}
	return nil
}

func (s *session) askNewKey() error {
	choice, err := s.env.UI.Select("Which public key do you want to add?", "", options(
		"Make a new key for me", keyGenerated,
		"I have an age public key", keyOwn,
	), keyGenerated)
	if err != nil {
		return err
	}
	return s.addKey(choice)
}

func (s *session) askMoreKeys() error {
	for {
		more, err := s.env.UI.Confirm("Add another public key?",
			"Each public key can decrypt all backups. For example, add a key for a team member or an emergency key.", false)
		if err != nil || !more {
			return err
		}
		if err := s.askNewKey(); err != nil {
			return err
		}
	}
}

func (s *session) addKey(choice string) error {
	var public string
	var err error
	if choice == keyGenerated {
		public, err = s.generateAgeKey()
	} else {
		public, err = s.askAgePublicKey()
	}
	if err != nil {
		return err
	}
	s.cfg.Encryption.Recipients = append(s.cfg.Encryption.Recipients, public)
	return nil
}

func (s *session) generateAgeKey() (string, error) {
	ui := s.env.UI
	pair, err := encrypt.GenerateKey()
	if err != nil {
		return "", err
	}
	ui.Warn("Save this private key in a password manager now. Without this key, nobody can restore the backups. dbak does not keep a copy.")
	ui.Code(pair.Private)
	for {
		pasted, err := ui.Secret("Paste the private key", "dbak makes sure that you saved the full key.", required)
		if err != nil {
			return "", err
		}
		err = encrypt.CheckPrivateKey(pasted, pair.Public)
		if err == nil {
			break
		}
		ui.Error(app.Sentence(err.Error()))
		choice, err := ui.Select("What do you want to do?", "", options(
			"Paste the key again", keyRetry,
			"Show the key again", keyShow,
		), keyRetry)
		if err != nil {
			return "", err
		}
		if choice == keyShow {
			ui.Code(pair.Private)
		}
	}
	if s.env.ClearScreen != nil {
		s.env.ClearScreen()
	}
	ui.Success("The private key is correct. The test encryption and decryption passed.")
	ui.Info("dbak keeps only the public key: " + pair.Public)
	return pair.Public, nil
}

func (s *session) askAgePublicKey() (string, error) {
	key, err := s.env.UI.Input("Age public key",
		"Run age-keygen on a different computer. Paste the key after \"Public key:\". The key starts with age1.",
		"", s.validNewRecipient)
	return strings.TrimSpace(key), err
}

func (s *session) validNewRecipient(value string) error {
	value = strings.TrimSpace(value)
	if _, err := encrypt.ParseRecipient(value); err != nil {
		return err
	}
	if slices.Contains(s.cfg.Encryption.Recipients, value) {
		return errors.New("the config has this public key already. Type a different key")
	}
	return nil
}

func (s *session) removeKey() error {
	recipients := s.cfg.Encryption.Recipients
	opts := make([]Option, 0, len(recipients))
	for i, key := range recipients {
		opts = append(opts, Option{Label: key, Value: strconv.Itoa(i)})
	}
	choice, err := s.env.UI.Select("Select the public key to remove.",
		"The backups that dbak made before stay encrypted for this key too.", opts, opts[0].Value)
	if err != nil {
		return err
	}
	i, _ := strconv.Atoi(choice)
	s.cfg.Encryption.Recipients = slices.Delete(slices.Clone(recipients), i, i+1)
	return nil
}

func (s *session) turnOffEncryption() error {
	ok, err := s.env.UI.Confirm("Turn off the encryption?",
		"The next backups will not be encrypted. The old backups stay encrypted. Keep the private key to restore them.", false)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAborted
	}
	s.cfg.Encryption.Recipients = nil
	s.env.UI.Warn("The backups will not be encrypted. A person with access to the SFTP server can read them.")
	return nil
}

func describeEncryption(recipients []string) string {
	if len(recipients) == 0 {
		return "off"
	}
	return "on, " + keyCount(len(recipients))
}

func keyCount(n int) string {
	if n == 1 {
		return "1 public key"
	}
	return fmt.Sprintf("%d public keys", n)
}
