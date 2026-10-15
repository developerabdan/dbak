package app

import "testing"

func TestSentence(t *testing.T) {
	cases := map[string]string{
		"the file is missing. Make it": "The file is missing. Make it.",
		"Already a sentence.":          "Already a sentence.",
		"":                             "",
		"dbak init must run as root":   "dbak init must run as root.",
		"pg_dump stopped with code 1":  "pg_dump stopped with code 1.",
		"mysqldump: Got error: 1045":   "mysqldump: Got error: 1045.",
		"systemd does not accept it":   "systemd does not accept it.",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := Sentence(in); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}
