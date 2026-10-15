package app

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"no error gives zero", nil, ExitOK},
		{"plain error gives general code", errors.New("x"), ExitGeneral},
		{"exit error gives its code", Exit(ExitLocked, errors.New("x")), ExitLocked},
		{"wrapped exit error gives its code", fmt.Errorf("wrap: %w", Exit(ExitPartial, nil)), ExitPartial},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExitCode(c.err); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}
