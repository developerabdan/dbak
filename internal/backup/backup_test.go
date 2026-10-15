package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/encrypt"
	"github.com/developerabdan/dbak/internal/engine"
	"github.com/developerabdan/dbak/internal/state"
)

type fakeEngine struct {
	scripts  map[string]string
	cleanups int
}

func (f *fakeEngine) Name() string          { return "fake" }
func (f *fakeEngine) FileExtension() string { return "dump" }
func (f *fakeEngine) Probe(context.Context, string) (engine.ServerInfo, error) {
	return engine.ServerInfo{}, nil
}
func (f *fakeEngine) WrapOutput(w io.Writer) (io.WriteCloser, error) {
	return engine.NopWriteCloser(w), nil
}
func (f *fakeEngine) DumpCommand(ctx context.Context, database string) (*engine.Dump, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", f.scripts[database])
	return engine.NewDump(cmd, func() error { f.cleanups++; return nil }), nil
}

type fakeStore struct {
	mu        sync.Mutex
	files     map[string]*bytes.Buffer
	dirs      []string
	failAfter int
	failMkdir bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{files: map[string]*bytes.Buffer{}, failAfter: -1}
}

func (s *fakeStore) MkdirAll(dir string) error {
	if s.failMkdir {
		return errors.New("permission denied")
	}
	s.dirs = append(s.dirs, dir)
	return nil
}

func (s *fakeStore) Create(path string) (io.WriteCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := &bytes.Buffer{}
	s.files[path] = buf
	return &fakeFile{store: s, buf: buf}, nil
}

func (s *fakeStore) Rename(oldPath, newPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[newPath] = s.files[oldPath]
	delete(s.files, oldPath)
	return nil
}

func (s *fakeStore) Remove(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, path)
	return nil
}

type fakeFile struct {
	store *fakeStore
	buf   *bytes.Buffer
}

func (f *fakeFile) Write(p []byte) (int, error) {
	if f.store.failAfter >= 0 && f.buf.Len()+len(p) > f.store.failAfter {
		return 0, errors.New("connection lost")
	}
	return f.buf.Write(p)
}

func (f *fakeFile) Close() error { return nil }

func newRunner(e engine.Engine, s Store) *Runner {
	at := time.Date(2026, 10, 15, 19, 0, 0, 0, time.UTC)
	return &Runner{
		Engine:     e,
		Store:      s,
		RemotePath: "/backups",
		Hostname:   "vm-app-01",
		Source:     "main_pg",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        func() time.Time { return at },
	}
}

func TestFileNaming(t *testing.T) {
	at := time.Date(2026, 10, 16, 2, 0, 0, 0, time.FixedZone("WIB", 7*3600))
	got := RemoteFile("/backups", "vm-app-01", "main_pg", "app_prod", at, "dump")
	if want := "/backups/vm-app-01/main_pg/app_prod/app_prod_20261015T190000Z.dump"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if got := FileName("app_prod", at, "sql.zst"); got != "app_prod_20261015T190000Z.sql.zst" {
		t.Fatalf("got %s", got)
	}
}

func TestSuccessfulBackup(t *testing.T) {
	e := &fakeEngine{scripts: map[string]string{"app_prod": "printf 'dump data'"}}
	s := newFakeStore()
	results := newRunner(e, s).Run(context.Background(), []string{"app_prod"})
	final := "/backups/vm-app-01/main_pg/app_prod/app_prod_20261015T190000Z.dump"
	if results[0].Status != state.StatusSuccess || results[0].Source != "main_pg" || results[0].RemotePath != final || results[0].SizeBytes != 9 {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	if got := s.files[final]; got == nil || got.String() != "dump data" {
		t.Fatalf("the final file has wrong content: %v", got)
	}
	if _, ok := s.files[final+PartialSuffix]; ok {
		t.Fatal("the partial file exists after success")
	}
	if s.dirs[0] != "/backups/vm-app-01/main_pg/app_prod" {
		t.Fatalf("got directory %s", s.dirs[0])
	}
	if e.cleanups != 1 {
		t.Fatalf("got %d cleanups, want 1", e.cleanups)
	}
}

func TestFailedDumpRemovesPartialFile(t *testing.T) {
	e := &fakeEngine{scripts: map[string]string{
		"app_prod": "printf 'half'; echo 'pg_dump: error: permission denied for table audit_log' >&2; exit 1",
	}}
	s := newFakeStore()
	results := newRunner(e, s).Run(context.Background(), []string{"app_prod"})
	res := results[0]
	if res.Status != state.StatusFailure || res.FailureKind != FailureDump {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.Error, "exit code 1") || !strings.Contains(res.Error, "permission denied for table audit_log") {
		t.Fatalf("the error does not show the cause: %s", res.Error)
	}
	if len(s.files) != 0 {
		t.Fatalf("files exist after a failed dump: %v", s.files)
	}
	if e.cleanups != 1 {
		t.Fatalf("got %d cleanups, want 1", e.cleanups)
	}
}

func TestUploadFailureStopsDump(t *testing.T) {
	e := &fakeEngine{scripts: map[string]string{"app_prod": "yes backup"}}
	s := newFakeStore()
	s.failAfter = 3 << 20
	done := make(chan []Result)
	go func() {
		done <- newRunner(e, s).Run(context.Background(), []string{"app_prod"})
	}()
	select {
	case results := <-done:
		if results[0].FailureKind != FailureUpload || !strings.Contains(results[0].Error, "connection lost") {
			t.Fatalf("unexpected result: %+v", results[0])
		}
		if len(s.files) != 0 {
			t.Fatal("the partial file exists after an upload failure")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not stop after the upload failure")
	}
}

func TestMissingToolGivesClearError(t *testing.T) {
	e := &missingToolEngine{}
	results := newRunner(e, newFakeStore()).Run(context.Background(), []string{"app_prod"})
	if !strings.Contains(results[0].Error, "Make sure that the client tool is installed") {
		t.Fatalf("got %s", results[0].Error)
	}
}

type missingToolEngine struct{ fakeEngine }

func (m *missingToolEngine) DumpCommand(ctx context.Context, _ string) (*engine.Dump, error) {
	return engine.NewDump(exec.CommandContext(ctx, "/nonexistent/pg_dump"), nil), nil
}

func TestOneFailureDoesNotStopOtherDatabases(t *testing.T) {
	e := &fakeEngine{scripts: map[string]string{
		"app_prod":  "exit 2",
		"app_audit": "printf ok",
	}}
	results := newRunner(e, newFakeStore()).Run(context.Background(), []string{"app_prod", "app_audit"})
	if results[0].Status != state.StatusFailure || results[1].Status != state.StatusSuccess {
		t.Fatalf("unexpected results: %+v", results)
	}
	if ExitCode(results) != app.ExitPartial {
		t.Fatalf("got exit code %d, want %d", ExitCode(results), app.ExitPartial)
	}
}

func TestMkdirFailureIsUploadFailure(t *testing.T) {
	e := &fakeEngine{scripts: map[string]string{"app_prod": "printf ok"}}
	s := newFakeStore()
	s.failMkdir = true
	results := newRunner(e, s).Run(context.Background(), []string{"app_prod"})
	if results[0].FailureKind != FailureUpload {
		t.Fatalf("unexpected result: %+v", results[0])
	}
}

func TestExitCode(t *testing.T) {
	ok := Result{Database: state.Database{Status: state.StatusSuccess}}
	dump := Result{Database: state.Database{Status: state.StatusFailure}, FailureKind: FailureDump}
	upload := Result{Database: state.Database{Status: state.StatusFailure}, FailureKind: FailureUpload}
	connect := Result{Database: state.Database{Status: state.StatusFailure}, FailureKind: FailureConnect}
	badConfig := Result{Database: state.Database{Status: state.StatusFailure}, FailureKind: FailureConfig}
	cases := []struct {
		name    string
		results []Result
		want    int
	}{
		{"all databases succeed", []Result{ok, ok}, app.ExitOK},
		{"some databases fail", []Result{ok, dump}, app.ExitPartial},
		{"all dumps fail", []Result{dump, dump}, app.ExitDumpFailed},
		{"all uploads fail", []Result{upload, upload}, app.ExitUploadFailed},
		{"mixed failures", []Result{upload, dump}, app.ExitDumpFailed},
		{"all connections fail", []Result{connect, connect}, app.ExitDatabase},
		{"one source fails to connect", []Result{ok, connect}, app.ExitPartial},
		{"all password files are bad", []Result{badConfig}, app.ExitConfigInvalid},
		{"connection and dump failures", []Result{connect, dump}, app.ExitDumpFailed},
		{"no results", nil, app.ExitDumpFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExitCode(c.results); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestEncryptedBackup(t *testing.T) {
	key, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	recipients, err := encrypt.ParseRecipients([]string{key.Public})
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeEngine{scripts: map[string]string{"app_prod": "printf 'dump data'"}}
	s := newFakeStore()
	runner := newRunner(e, s)
	runner.Recipients = recipients
	results := runner.Run(context.Background(), []string{"app_prod"})
	final := "/backups/vm-app-01/main_pg/app_prod/app_prod_20261015T190000Z.dump.age"
	if results[0].Status != state.StatusSuccess || results[0].RemotePath != final {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	sealed := s.files[final]
	if sealed == nil || int64(sealed.Len()) != results[0].SizeBytes {
		t.Fatalf("the size %d is not the size of the encrypted file", results[0].SizeBytes)
	}
	if strings.Contains(sealed.String(), "dump data") {
		t.Fatal("the uploaded file contains plain data")
	}
	identity, err := age.ParseX25519Identity(key.Private)
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(sealed.Bytes()), identity)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(r)
	if err != nil || string(plain) != "dump data" {
		t.Fatalf("got %q %v after decryption", plain, err)
	}
}

type closingEngine struct {
	fakeEngine
}

type writeOnClose struct {
	w   io.Writer
	buf bytes.Buffer
}

func (c *writeOnClose) Write(p []byte) (int, error) { return c.buf.Write(p) }
func (c *writeOnClose) Close() error {
	_, err := c.w.Write(append([]byte("compressed:"), c.buf.Bytes()...))
	return err
}

func (e *closingEngine) WrapOutput(w io.Writer) (io.WriteCloser, error) {
	return &writeOnClose{w: w}, nil
}

func TestEncryptionClosesAfterTheEngineOutput(t *testing.T) {
	key, err := encrypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	recipients, err := encrypt.ParseRecipients([]string{key.Public})
	if err != nil {
		t.Fatal(err)
	}
	e := &closingEngine{fakeEngine{scripts: map[string]string{"shop": "printf 'rows'"}}}
	s := newFakeStore()
	runner := newRunner(e, s)
	runner.Recipients = recipients
	results := runner.Run(context.Background(), []string{"shop"})
	if results[0].Status != state.StatusSuccess {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	identity, err := age.ParseX25519Identity(key.Private)
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(s.files[results[0].RemotePath].Bytes()), identity)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(r)
	if err != nil || string(plain) != "compressed:rows" {
		t.Fatalf("got %q %v, want the data that the engine wrote at close", plain, err)
	}
}
