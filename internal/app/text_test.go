package app

import "testing"

func TestSentence(t *testing.T) {
	cases := map[string]string{
		"the file is missing. Make it": "The file is missing. Make it.",
		"Already a sentence.":          "Already a sentence.",
		"":                             "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := Sentence(in); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}
