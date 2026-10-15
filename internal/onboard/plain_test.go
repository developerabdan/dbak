package onboard

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
)

func plainUI(input string) (*PlainUI, *bytes.Buffer) {
	var out bytes.Buffer
	return NewPlainUI(strings.NewReader(input), &out, nil), &out
}

func TestPlainInputKeepsDefaultAndValidates(t *testing.T) {
	ui, out := plainUI("\nabc\n70000\n2222\n")
	got, err := ui.Input("Host", "", "127.0.0.1", required)
	if err != nil || got != "127.0.0.1" {
		t.Fatalf("got %q %v", got, err)
	}
	got, err = ui.Input("Port", "", "22", validPort)
	if err != nil || got != "2222" {
		t.Fatalf("got %q %v", got, err)
	}
	if strings.Count(out.String(), "The value is not valid") != 2 {
		t.Fatalf("got output %q", out.String())
	}
}

func TestPlainSelect(t *testing.T) {
	ui, _ := plainUI("\n9\n2\n")
	opts := options("A", "a", "B", "b")
	got, err := ui.Select("Pick", "", opts, "a")
	if err != nil || got != "a" {
		t.Fatalf("got %q %v", got, err)
	}
	got, err = ui.Select("Pick", "", opts, "a")
	if err != nil || got != "b" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestPlainMultiSelect(t *testing.T) {
	ui, _ := plainUI("\n3, 1\n")
	opts := sameOptions("x", "y", "z")
	got, err := ui.MultiSelect("Pick", "", opts, []string{"y"}, nil)
	if err != nil || !slices.Equal(got, []string{"y"}) {
		t.Fatalf("got %v %v", got, err)
	}
	got, err = ui.MultiSelect("Pick", "", opts, []string{"y"}, nil)
	if err != nil || !slices.Equal(got, []string{"z", "x"}) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestPlainConfirm(t *testing.T) {
	ui, _ := plainUI("\nmaybe\nn\nYES\n")
	if got, _ := ui.Confirm("Q", "", true); !got {
		t.Fatal("got false for the default true")
	}
	if got, _ := ui.Confirm("Q", "", true); got {
		t.Fatal("got true for n")
	}
	if got, _ := ui.Confirm("Q", "", false); !got {
		t.Fatal("got false for YES")
	}
}

func TestPlainSecretUsesReader(t *testing.T) {
	var out bytes.Buffer
	ui := NewPlainUI(strings.NewReader(""), &out, func(string) (string, error) { return "hidden", nil })
	got, err := ui.Secret("Password", "", required)
	if err != nil || got != "hidden" || strings.Contains(out.String(), "hidden") {
		t.Fatalf("got %q %v %q", got, err, out.String())
	}
}

func TestPlainEndOfInputStops(t *testing.T) {
	ui, _ := plainUI("")
	if _, err := ui.Input("Host", "", "", required); !errors.Is(err, ErrAborted) {
		t.Fatalf("got %v, want ErrAborted", err)
	}
	ui, _ = plainUI("last")
	got, err := ui.Input("Host", "", "", required)
	if err != nil || got != "last" {
		t.Fatalf("got %q %v for a last line without a newline", got, err)
	}
}

func TestPlainEchoHidesSecret(t *testing.T) {
	var out bytes.Buffer
	ui := NewPlainUI(strings.NewReader("host\npass\n"), &out, nil)
	ui.echo = true
	if _, err := ui.Input("Host", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ui.Secret("Password", "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "host\n") || strings.Contains(out.String(), "pass\n") {
		t.Fatalf("got output %q", out.String())
	}
}
