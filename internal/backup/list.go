package backup

import (
	"errors"
	"os"
	"path"
	"slices"
	"time"

	"github.com/developerabdan/dbak/internal/retention"
)

type Entry struct {
	Source    string    `json:"source"`
	Database  string    `json:"database"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	SizeBytes int64     `json:"size_bytes"`
	Time      time.Time `json:"time"`
}

type Lister interface {
	ReadDir(dir string) ([]os.FileInfo, error)
}

func ListBackups(store Lister, remotePath, hostname, source, database string) ([]Entry, error) {
	dir := RemoteDir(remotePath, hostname, source, database)
	infos, err := store.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, info := range infos {
		if !info.Mode().IsRegular() {
			continue
		}
		f, ok := retention.Parse(database, info.Name())
		if !ok || f.Partial {
			continue
		}
		entries = append(entries, Entry{
			Source:    source,
			Database:  database,
			Name:      f.Name,
			Path:      path.Join(dir, f.Name),
			SizeBytes: info.Size(),
			Time:      f.Time,
		})
	}
	slices.SortFunc(entries, func(a, b Entry) int { return b.Time.Compare(a.Time) })
	return entries, nil
}
