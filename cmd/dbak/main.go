package main

import (
	"fmt"
	"os"

	"dbak/internal/app"
)

func main() {
	err := newRootCommand().Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: "+app.Sentence(err.Error()))
	}
	os.Exit(app.ExitCode(err))
}
