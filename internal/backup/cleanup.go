package backup

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

const StaleCredentialAge = time.Hour

var credentialPatterns = []string{"pgpass-*", "my-*.cnf"}

func RemoveStaleCredentials(dir string, now time.Time) ([]string, error) {
	var removed []string
	for _, pattern := range credentialPatterns {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return removed, err
		}
		for _, file := range matches {
			info, err := os.Lstat(file)
			if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < StaleCredentialAge {
				continue
			}
			if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
			removed = append(removed, file)
		}
	}
	return removed, nil
}
