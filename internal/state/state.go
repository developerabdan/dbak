package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/developerabdan/dbak/internal/config"
)

const MaxRuns = 50

const (
	StatusSuccess = "success"
	StatusPartial = "partial"
	StatusFailure = "failure"
)

type Run struct {
	Host       string     `json:"host"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt time.Time  `json:"finished_at"`
	Databases  []Database `json:"databases"`
}

type Database struct {
	Source     string        `json:"source,omitempty"`
	Name       string        `json:"name"`
	Status     string        `json:"status"`
	SizeBytes  int64         `json:"size_bytes,omitempty"`
	Duration   time.Duration `json:"duration_ns,omitempty"`
	RemotePath string        `json:"remote_path,omitempty"`
	Error      string        `json:"error,omitempty"`
}

type File struct {
	Runs []Run `json:"runs"`
}

func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the state file %s: %w", path, err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("the state file %s is not valid JSON: %w. Remove the file to start a new history", path, err)
	}
	return &f, nil
}

func Append(path string, run Run) error {
	f, err := Load(path)
	if err != nil {
		return err
	}
	f.Runs = append(f.Runs, run)
	if len(f.Runs) > MaxRuns {
		f.Runs = f.Runs[len(f.Runs)-MaxRuns:]
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode the state: %w", err)
	}
	return config.WriteFileAtomic(path, data, 0o600)
}

func OverallStatus(databases []Database) string {
	succeeded := 0
	for _, db := range databases {
		if db.Status == StatusSuccess {
			succeeded++
		}
	}
	switch {
	case len(databases) > 0 && succeeded == len(databases):
		return StatusSuccess
	case succeeded == 0:
		return StatusFailure
	default:
		return StatusPartial
	}
}
