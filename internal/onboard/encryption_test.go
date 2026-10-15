package onboard

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/encrypt"
)

var privateKeyPattern = regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Z]+`)

func (h *harness) shownPrivateKey() func() string {
	return func() string {
		keys := privateKeyPattern.FindAllString(h.ui.out.String(), -1)
		if len(keys) == 0 {
			return ""
		}
		return keys[len(keys)-1]
	}
}

func generatedKeyAnswers(h *harness) []answer {
	return []answer{
		{match: "Encrypt the backups?", value: keyGenerated},
		{match: "Paste the private key", value: h.shownPrivateKey()},
		{match: "Add another public key?", value: false},
	}
}

func replaceAnswer(answers []answer, match string, with ...answer) []answer {
	for i, a := range answers {
		if a.match == match {
			out := append([]answer{}, answers[:i]...)
			out = append(out, with...)
			return append(out, answers[i+1:]...)
		}
	}
	panic("no answer for " + match)
}

func TestEncryptionWithAGeneratedKey(t *testing.T) {
	h := newHarness(t)
	cleared := 0
	h.env.ClearScreen = func() { cleared++ }
	other, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	h.ui = newFakeUI(t,
		answer{match: "Encrypt the backups?", value: keyGenerated},
		answer{match: "Paste the private key", value: "AGE-SECRET-KEY-1WRONG"},
		answer{match: "What do you want to do?", value: keyShow},
		answer{match: "Paste the private key", value: other.Private},
		answer{match: "What do you want to do?", value: keyRetry},
		answer{match: "Paste the private key", value: h.shownPrivateKey()},
		answer{match: "Add another public key?", value: true},
		answer{match: "Which public key", value: keyOwn},
		answer{match: "Age public key", value: other.Public},
		answer{match: "Add another public key?", value: false},
	)
	h.env.UI = h.ui
	s := newSession(h.env)
	if err := s.configureEncryption(); err != nil {
		t.Fatal(err)
	}
	h.ui.done()
	recipients := s.cfg.Encryption.Recipients
	if len(recipients) != 2 || recipients[1] != other.Public {
		t.Fatalf("got recipients %v", recipients)
	}
	shown := h.shownPrivateKey()()
	if err := encrypt.CheckPrivateKey(shown, recipients[0]); err != nil {
		t.Fatalf("the shown private key does not match the saved public key: %v", err)
	}
	out := h.ui.out.String()
	for _, want := range []string{
		"WARNING: Save this private key in a password manager now.",
		"ERROR: The value is not a valid age private key.",
		"ERROR: The private key does not match the public key.",
		"OK: The private key is correct. The test encryption and decryption passed.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not contain %q:\n%s", want, out)
		}
	}
	if len(privateKeyPattern.FindAllString(out, -1)) != 2 {
		t.Fatalf("dbak must show the key one time and again after the request:\n%s", out)
	}
	if cleared != 1 {
		t.Fatalf("got %d screen clears, want 1", cleared)
	}
}

func TestEncryptionDeclined(t *testing.T) {
	h := newHarness(t)
	h.ui = newFakeUI(t, answer{match: "Encrypt the backups?", value: keyOff})
	h.env.UI = h.ui
	s := newSession(h.env)
	if err := s.configureEncryption(); err != nil {
		t.Fatal(err)
	}
	if s.cfg.Encryption.Enabled() || !strings.Contains(h.ui.out.String(), "WARNING: The backups will not be encrypted.") {
		t.Fatalf("got %+v\n%s", s.cfg.Encryption, h.ui.out.String())
	}
}

func TestFullOnboardingWithEncryptionKeepsNoPrivateKey(t *testing.T) {
	h := newHarness(t)
	answers := h.onePostgresSource()
	answers = append(answers, h.destinationAnswers(t)...)
	answers = append(answers, replaceAnswer(tailAnswers(), "Encrypt the backups?", generatedKeyAnswers(h)...)...)
	answers = append(answers, answer{match: "Run the first backup now?", value: false})
	if err := h.run(t, answers...); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, h.ui.out.String())
	}
	cfg := loadSaved(t, h)
	if len(cfg.Encryption.Recipients) != 1 {
		t.Fatalf("got recipients %v", cfg.Encryption.Recipients)
	}
	if !strings.Contains(h.ui.out.String(), "Encryption:        on, 1 public key") {
		t.Fatalf("the summary does not show the encryption:\n%s", h.ui.out.String())
	}
	root := filepath.Dir(filepath.Dir(h.env.Paths.ConfigPath))
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "AGE-SECRET-KEY") {
			t.Errorf("the file %s contains the private key", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEditEncryption(t *testing.T) {
	first, second, third := mustKey(t), mustKey(t), mustKey(t)
	cases := []struct {
		name    string
		answers []answer
		want    []string
		err     error
	}{
		{"keep", []answer{{match: "What do you want to change?", value: keyKeep}}, []string{first.Public, second.Public}, nil},
		{"remove", []answer{
			{match: "What do you want to change?", value: keyRemove},
			{match: "Select the public key to remove.", value: "0"},
		}, []string{second.Public}, nil},
		{"turn off", []answer{
			{match: "What do you want to change?", value: keyOff},
			{match: "Turn off the encryption?", value: true},
		}, nil, nil},
		{"turn off declined", []answer{
			{match: "What do you want to change?", value: keyOff},
			{match: "Turn off the encryption?", value: false},
		}, []string{first.Public, second.Public}, ErrAborted},
		{"add", []answer{
			{match: "What do you want to change?", value: keyAdd},
			{match: "Which public key", value: keyOwn},
			{match: "Age public key", value: " " + third.Public + "\n"},
		}, []string{first.Public, second.Public, third.Public}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.ui = newFakeUI(t, c.answers...)
			h.env.UI = h.ui
			s := newSession(h.env)
			s.cfg.Encryption.Recipients = []string{first.Public, second.Public}
			err := s.configureEncryption()
			if !errors.Is(err, c.err) {
				t.Fatalf("got %v, want %v", err, c.err)
			}
			if c.err != nil {
				return
			}
			if strings.Join(s.cfg.Encryption.Recipients, ",") != strings.Join(c.want, ",") {
				t.Fatalf("got %v, want %v", s.cfg.Encryption.Recipients, c.want)
			}
		})
	}
}

func mustKey(t *testing.T) encrypt.KeyPair {
	t.Helper()
	pair, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func TestValidNewRecipient(t *testing.T) {
	h := newHarness(t)
	s := newSession(h.env)
	key := mustKey(t)
	s.cfg.Encryption.Recipients = []string{key.Public}
	if err := s.validNewRecipient(key.Public); err == nil {
		t.Fatal("got no error for a key that the config has")
	}
	if err := s.validNewRecipient(key.Private); err == nil {
		t.Fatal("got no error for a private key")
	}
	if err := s.validNewRecipient(mustKey(t).Public); err != nil {
		t.Fatal(err)
	}
}

func TestBatchInitWithEncryption(t *testing.T) {
	key := mustKey(t)
	cases := []struct {
		name       string
		recipients []string
		code       int
	}{
		{"valid key", []string{key.Public}, app.ExitOK},
		{"private key instead of public key", []string{key.Private}, app.ExitConfigInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			input := h.batchInput(t, func(cfg *config.Config) { cfg.Encryption.Recipients = c.recipients })
			err := h.runBatch(t, input, Secrets{DBPassword: "x"})
			if app.ExitCode(err) != c.code {
				t.Fatalf("got exit code %d: %v", app.ExitCode(err), err)
			}
			if err != nil && strings.Contains(err.Error(), "AGE-SECRET-KEY") {
				t.Fatal("the error contains the private key")
			}
			if c.code == app.ExitOK && loadSaved(t, h).Encryption.Recipients[0] != key.Public {
				t.Fatal("the saved config does not have the public key")
			}
		})
	}
}
