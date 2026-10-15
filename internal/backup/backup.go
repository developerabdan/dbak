package backup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path"
	"path/filepath"
	"time"

	"filippo.io/age"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/encrypt"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/state"
)

const (
	FailureConfig  = "config"
	FailureConnect = "connect"
	FailureDump    = "dump"
	FailureUpload  = "upload"
)

var failureExitCodes = map[string]int{
	FailureConfig:  app.ExitConfigInvalid,
	FailureConnect: app.ExitDatabase,
	FailureDump:    app.ExitDumpFailed,
	FailureUpload:  app.ExitUploadFailed,
}

const uploadBufferSize = 1 << 20

type Store interface {
	MkdirAll(dir string) error
	Create(path string) (io.WriteCloser, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error
}

type Result struct {
	state.Database
	FailureKind string
}

type Runner struct {
	Engine     engine.Engine
	Store      Store
	RemotePath string
	Hostname   string
	Source     string
	Recipients []age.Recipient
	Logger     *slog.Logger
	Now        func() time.Time
}

func (r *Runner) encrypted() bool {
	return len(r.Recipients) > 0
}

func (r *Runner) extension() string {
	if r.encrypted() {
		return r.Engine.FileExtension() + "." + encrypt.Extension
	}
	return r.Engine.FileExtension()
}

func (r *Runner) Run(ctx context.Context, databases []string) []Result {
	results := make([]Result, 0, len(databases))
	for _, database := range databases {
		results = append(results, r.runOne(ctx, database))
	}
	return results
}

func (r *Runner) runOne(ctx context.Context, database string) Result {
	started := r.now()
	final := RemoteFile(r.RemotePath, r.Hostname, r.Source, database, started, r.extension())
	partial := final + PartialSuffix
	r.Logger.Info(fmt.Sprintf("Backup of %s started.", database), "database", database)
	size, created, err := r.upload(ctx, database, partial)
	if err == nil {
		err = r.Store.Rename(partial, final)
		if err != nil {
			err = &uploadError{err}
		}
	}
	if err != nil {
		if created {
			r.removePartial(partial)
		}
		return r.failure(database, err)
	}
	duration := r.now().Sub(started)
	r.Logger.Info(fmt.Sprintf("Backup of %s completed.", database),
		"database", database, "size_bytes", size, "duration", duration.Round(time.Millisecond).String(), "encrypted", r.encrypted(), "remote_path", final)
	return Result{Database: state.Database{
		Source:     r.Source,
		Name:       database,
		Status:     state.StatusSuccess,
		SizeBytes:  size,
		Duration:   duration,
		RemotePath: final,
	}}
}

func (r *Runner) failure(database string, err error) Result {
	kind := FailureDump
	var upErr *uploadError
	if errors.As(err, &upErr) {
		kind = FailureUpload
	}
	r.Logger.Error(fmt.Sprintf("Backup of %s failed.", database), "database", database, "error", err.Error())
	return Result{
		Database:    state.Database{Source: r.Source, Name: database, Status: state.StatusFailure, Error: err.Error()},
		FailureKind: kind,
	}
}

func (r *Runner) removePartial(partial string) {
	if err := r.Store.Remove(partial); err != nil {
		r.Logger.Warn("Cannot remove the partial file. The next retention run will remove it.", "path", partial, "error", err.Error())
	}
}

func (r *Runner) upload(ctx context.Context, database, partial string) (int64, bool, error) {
	if err := r.Store.MkdirAll(path.Dir(partial)); err != nil {
		return 0, false, &uploadError{err}
	}
	dump, err := r.Engine.DumpCommand(ctx, database)
	if err != nil {
		return 0, false, err
	}
	defer func() {
		if err := dump.Cleanup(); err != nil {
			r.Logger.Warn("Cannot remove a temporary credentials file. Remove it manually.", "error", err.Error())
		}
	}()
	file, err := r.Store.Create(partial)
	if err != nil {
		return 0, false, &uploadError{err}
	}
	counter := &countingWriter{w: file}
	size, streamErr := r.stream(dump.Cmd, counter)
	if closeErr := file.Close(); streamErr == nil && closeErr != nil {
		streamErr = &uploadError{fmt.Errorf("cannot close %s: %w", partial, closeErr)}
	}
	return size, true, streamErr
}

func (r *Runner) stream(cmd *exec.Cmd, counter *countingWriter) (int64, error) {
	buffered := bufio.NewWriterSize(counter, uploadBufferSize)
	var sink io.Writer = buffered
	var sealer io.WriteCloser
	if r.encrypted() {
		var err error
		if sealer, err = encrypt.NewWriter(buffered, r.Recipients); err != nil {
			return 0, err
		}
		sink = sealer
	}
	out, err := r.Engine.WrapOutput(sink)
	if err != nil {
		return 0, err
	}
	stderr := engine.NewTailBuffer()
	cmd.Stderr = stderr
	cmd.WaitDelay = engine.WaitDelay
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	tool := filepath.Base(cmd.Path)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("cannot start %s: %w. Make sure that the client tool is installed", tool, err)
	}
	_, copyErr := io.Copy(out, stdout)
	if copyErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if copyErr != nil {
		return 0, copyErr
	}
	if waitErr != nil {
		return 0, engine.CommandError(tool, waitErr, stderr.LastLine())
	}
	if err := out.Close(); err != nil {
		return 0, err
	}
	if sealer != nil {
		if err := sealer.Close(); err != nil {
			return 0, err
		}
	}
	if err := buffered.Flush(); err != nil {
		return 0, err
	}
	return counter.n, nil
}

func ExitCode(results []Result) int {
	succeeded := 0
	kinds := map[string]bool{}
	for _, res := range results {
		if res.Status == state.StatusSuccess {
			succeeded++
			continue
		}
		kinds[res.FailureKind] = true
	}
	switch {
	case len(results) > 0 && succeeded == len(results):
		return app.ExitOK
	case succeeded > 0:
		return app.ExitPartial
	case len(kinds) == 1:
		for kind := range kinds {
			if code, ok := failureExitCodes[kind]; ok {
				return code
			}
		}
	}
	return app.ExitDumpFailed
}

func Databases(results []Result) []state.Database {
	out := make([]state.Database, 0, len(results))
	for _, res := range results {
		out = append(out, res.Database)
	}
	return out
}

func (r *Runner) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

type uploadError struct {
	err error
}

func (e *uploadError) Error() string {
	return e.err.Error()
}

func (e *uploadError) Unwrap() error {
	return e.err
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err != nil {
		return n, &uploadError{fmt.Errorf("the upload stopped: %w. Make sure that the SFTP server has free space and is reachable", err)}
	}
	return n, nil
}
