package status

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/schedule"
	"github.com/developerabdan/dbak/internal/state"
)

const never = "never"

type Report struct {
	Host       string     `json:"host,omitempty"`
	LastRun    *RunInfo   `json:"last_run"`
	Databases  []Database `json:"databases"`
	Schedule   Schedule   `json:"schedule"`
	Encryption Encryption `json:"encryption"`
}

type Encryption struct {
	Enabled    bool `json:"enabled"`
	PublicKeys int  `json:"public_keys"`
}

type RunInfo struct {
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type Database struct {
	Source     string     `json:"source"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	LastRunAt  *time.Time `json:"last_run_at"`
	SizeBytes  int64      `json:"size_bytes,omitempty"`
	DurationMS int64      `json:"duration_ms,omitempty"`
	RemotePath string     `json:"remote_path,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type Schedule struct {
	Description string     `json:"description"`
	Scheduler   string     `json:"scheduler"`
	Active      bool       `json:"active"`
	NextRun     *time.Time `json:"next_run"`
	Timezone    string     `json:"timezone"`
}

func Build(cfg *config.Config, history *state.File, installed schedule.Installed, now time.Time) (Report, error) {
	plan, err := schedule.NewPlan(cfg.Schedule)
	if err != nil {
		return Report{}, err
	}
	r := Report{
		Databases: []Database{},
		Schedule: Schedule{
			Description: plan.Describe(),
			Scheduler:   installed.Mode,
			Timezone:    cfg.Schedule.Timezone,
		},
		Encryption: Encryption{
			Enabled:    cfg.Encryption.Enabled(),
			PublicKeys: len(cfg.Encryption.Recipients),
		},
	}
	switch installed.Mode {
	case schedule.ModeSystemd:
		r.Schedule.Active = installed.Active == "active"
	case schedule.ModeCron:
		r.Schedule.Active = true
	}
	if r.Schedule.Active {
		next := plan.Next(now)
		r.Schedule.NextRun = &next
	}
	if n := len(history.Runs); n > 0 {
		last := history.Runs[n-1]
		r.Host = last.Host
		r.LastRun = &RunInfo{Status: last.Status, StartedAt: last.StartedAt, FinishedAt: last.FinishedAt}
	}
	for _, source := range cfg.Sources {
		for _, name := range source.Databases {
			r.Databases = append(r.Databases, lastResult(history, source.Name, name))
		}
	}
	return r, nil
}

func sameSource(recorded, source string) bool {
	return recorded == source || (recorded == "" && source == config.LegacySourceName)
}

func lastResult(history *state.File, source, name string) Database {
	for i := len(history.Runs) - 1; i >= 0; i-- {
		run := history.Runs[i]
		for _, db := range run.Databases {
			if db.Name != name || !sameSource(db.Source, source) {
				continue
			}
			at := run.StartedAt
			return Database{
				Source:     source,
				Name:       name,
				Status:     db.Status,
				LastRunAt:  &at,
				SizeBytes:  db.SizeBytes,
				DurationMS: db.Duration.Milliseconds(),
				RemotePath: db.RemotePath,
				Error:      db.Error,
			}
		}
	}
	return Database{Source: source, Name: name, Status: never}
}

func (r Report) WriteText(w io.Writer) error {
	loc, err := time.LoadLocation(r.Schedule.Timezone)
	if err != nil {
		loc = time.UTC
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SOURCE\tDATABASE\tSTATUS\tLAST RUN\tSIZE\tDURATION")
	for _, db := range r.Databases {
		last, size, duration := never, "-", "-"
		if db.LastRunAt != nil {
			last = formatTime(db.LastRunAt.In(loc))
		}
		if db.Status == state.StatusSuccess {
			size = app.HumanSize(db.SizeBytes)
			duration = (time.Duration(db.DurationMS) * time.Millisecond).String()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", db.Source, db.Name, db.Status, last, size, duration)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, db := range r.Databases {
		if db.Error != "" {
			fmt.Fprintf(w, "\n%s/%s: %s\n", db.Source, db.Name, app.Sentence(db.Error))
		}
	}
	fmt.Fprintln(w)
	if r.LastRun != nil {
		fmt.Fprintf(w, "Last run:     %s, %s\n", formatTime(r.LastRun.StartedAt.In(loc)), r.LastRun.Status)
	} else {
		fmt.Fprintln(w, "Last run:     never. To make a backup now, run: sudo -u dbak dbak run")
	}
	fmt.Fprintf(w, "Schedule:     %s\n", r.Schedule.Description)
	fmt.Fprintf(w, "Encryption:   %s\n", r.Encryption.text())
	switch {
	case r.Schedule.Scheduler == "":
		fmt.Fprintln(w, "Next backup:  none. No schedule is installed. Run: sudo dbak schedule")
	case !r.Schedule.Active:
		fmt.Fprintln(w, "Next backup:  none. The systemd timer is not active. Run: sudo dbak schedule")
	default:
		fmt.Fprintf(w, "Next backup:  %s (%s)\n", formatTime(r.Schedule.NextRun.In(loc)), r.Schedule.Scheduler)
	}
	return nil
}

func (e Encryption) text() string {
	switch {
	case !e.Enabled:
		return "off. To turn it on, run: sudo dbak init"
	case e.PublicKeys == 1:
		return "on, 1 public key"
	}
	return fmt.Sprintf("on, %d public keys", e.PublicKeys)
}

func formatTime(t time.Time) string {
	return strings.TrimSpace(t.Format("2006-01-02 15:04 MST"))
}
