package app

import (
	"strings"
	"unicode"
)

func Sentence(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return message
	}
	runes := []rune(message)
	runes[0] = unicode.ToUpper(runes[0])
	message = string(runes)
	if !strings.HasSuffix(message, ".") && !strings.HasSuffix(message, ":") {
		message += "."
	}
	return message
}
