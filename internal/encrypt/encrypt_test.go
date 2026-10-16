package encrypt

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestGenerateKeyAndCheck(t *testing.T) {
	pair, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pair.Private, "AGE-SECRET-KEY-1") || !strings.HasPrefix(pair.Public, "age1") {
		t.Fatalf("got %q and %q", pair.Private[:16], pair.Public)
	}
	if err := CheckPrivateKey("  "+pair.Private+"\n", pair.Public); err != nil {
		t.Fatalf("got %v for the correct key", err)
	}
	other, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateKey(other.Private, pair.Public); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("got %v, want ErrKeyMismatch", err)
	}
	if err := CheckPrivateKey("AGE-SECRET-KEY-1BAD", pair.Public); err == nil || errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("got %v, want a format error", err)
	}
}

func TestParseRecipient(t *testing.T) {
	pair, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	hybrid, err := age.GenerateHybridIdentity()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		value string
		err   string
	}{
		{"x25519 key", pair.Public, ""},
		{"key with spaces", " " + pair.Public + "\n", ""},
		{"post-quantum key", hybrid.Recipient().String(), "post-quantum"},
		{"ssh key", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl", "starts with age1"},
		{"empty", "", "starts with age1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseRecipient(c.value)
			if c.err == "" && err != nil {
				t.Fatalf("got %v", err)
			}
			if c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
				t.Fatalf("got %v, want an error with %q", err, c.err)
			}
		})
	}
}

func TestWriterOutputDecryptsWithEachKey(t *testing.T) {
	first, _ := GenerateKey()
	second, _ := GenerateKey()
	recipients, err := ParseRecipients([]string{first.Public, second.Public})
	if err != nil {
		t.Fatal(err)
	}
	var sealed bytes.Buffer
	w, err := NewWriter(&sealed, recipients)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("dump data ", 10000)
	if _, err := io.WriteString(w, payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(sealed.Bytes(), []byte("age-encryption.org/v1\n")) {
		t.Fatal("the output does not have the standard age header")
	}
	for _, key := range []KeyPair{first, second} {
		identity, err := age.ParseX25519Identity(key.Private)
		if err != nil {
			t.Fatal(err)
		}
		r, err := age.Decrypt(bytes.NewReader(sealed.Bytes()), identity)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil || string(got) != payload {
			t.Fatalf("the decrypted data is not the same: %v", err)
		}
	}
}

func TestNewWriterNeedsARecipient(t *testing.T) {
	if _, err := NewWriter(io.Discard, nil); err == nil {
		t.Fatal("got no error without a public key")
	}
}

func TestNewReaderDecryptsWithTheMatchingKey(t *testing.T) {
	pair, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	recipients, err := ParseRecipients([]string{pair.Public})
	if err != nil {
		t.Fatal(err)
	}
	var sealed bytes.Buffer
	w, err := NewWriter(&sealed, recipients)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "dump data"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("the matching key decrypts the data", func(t *testing.T) {
		identity, err := ParseIdentity("  " + pair.Private + "\n")
		if err != nil {
			t.Fatal(err)
		}
		r, err := NewReader(bytes.NewReader(sealed.Bytes()), identity)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil || string(got) != "dump data" {
			t.Fatalf("got %q and %v", got, err)
		}
	})

	t.Run("a different key gives ErrWrongKey", func(t *testing.T) {
		identity, err := ParseIdentity(other.Private)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewReader(bytes.NewReader(sealed.Bytes()), identity); !errors.Is(err, ErrWrongKey) {
			t.Fatalf("got %v, want ErrWrongKey", err)
		}
	})

	t.Run("a public key is not a private key", func(t *testing.T) {
		if _, err := ParseIdentity(pair.Public); err == nil {
			t.Fatal("got no error")
		}
	})
}
