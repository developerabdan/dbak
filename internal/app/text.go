package app

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

func Sentence(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return message
	}
	if !isCommandName(firstWord(message)) {
		runes := []rune(message)
		runes[0] = unicode.ToUpper(runes[0])
		message = string(runes)
	}
	if !strings.HasSuffix(message, ".") && !strings.HasSuffix(message, ":") {
		message += "."
	}
	return message
}

var commandNames = []string{Name, "mysqldump", "mysql", "psql", "systemd", "systemctl", "logrotate", "cron", "crond", "shuf"}

func firstWord(message string) string {
	word, _, _ := strings.Cut(message, " ")
	return strings.TrimRight(word, ".,:")
}

func isCommandName(word string) bool {
	return slices.Contains(commandNames, word) || strings.ContainsAny(word, "_-/")
}

func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
