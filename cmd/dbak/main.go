package main

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"dbak/internal/app"
)

func main() {
	err := newRootCommand().Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: "+sentence(err.Error()))
	}
	os.Exit(app.ExitCode(err))
}

func sentence(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return message
	}
	runes := []rune(message)
	runes[0] = unicode.ToUpper(runes[0])
	message = string(runes)
	if !strings.HasSuffix(message, ".") {
		message += "."
	}
	return message
}
