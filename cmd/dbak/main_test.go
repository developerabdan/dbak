package main

import "testing"

func TestSentence(t *testing.T) {
	if got := sentence("the file is missing. Make it"); got != "The file is missing. Make it." {
		t.Fatalf("got %q", got)
	}
}
