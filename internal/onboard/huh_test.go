package onboard

import (
	"bytes"
	"strings"
	"testing"
)

func TestAccessibleUIReadsPipedLines(t *testing.T) {
	in := strings.NewReader("first\n2\nsecond\ny\n")
	var out bytes.Buffer
	ui := NewTerminalUI(in, &out, true)
	first, err := ui.Input("First", "", "", nil)
	if err != nil || first != "first" {
		t.Fatalf("got %q %v", first, err)
	}
	choice, err := ui.Select("Choice", "", options("A", "a", "B", "b"), "a")
	if err != nil || choice != "b" {
		t.Fatalf("got %q %v", choice, err)
	}
	second, err := ui.Input("Second", "", "", nil)
	if err != nil || second != "second" {
		t.Fatalf("got %q %v", second, err)
	}
	ok, err := ui.Confirm("Confirm", "", false)
	if err != nil || !ok {
		t.Fatalf("got %v %v", ok, err)
	}
}
