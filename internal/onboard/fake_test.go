package onboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/developerabdan/dbak/internal/engine"
)

type answer struct {
	match string
	value any
	hook  func()
}

type fakeUI struct {
	t       *testing.T
	answers []answer
	out     strings.Builder
}

func newFakeUI(t *testing.T, answers ...answer) *fakeUI {
	return &fakeUI{t: t, answers: answers}
}

func (f *fakeUI) next(kind, title string) answer {
	f.t.Helper()
	if len(f.answers) == 0 {
		f.t.Fatalf("no answer for %s %q\noutput:\n%s", kind, title, f.out.String())
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	if !strings.Contains(title, a.match) {
		f.t.Fatalf("got %s %q, want a question with %q\noutput:\n%s", kind, title, a.match, f.out.String())
	}
	fmt.Fprintf(&f.out, "? %s\n", title)
	if a.hook != nil {
		a.hook()
	}
	return a
}

func (f *fakeUI) check(title string, validate func(string) error, value string) {
	f.t.Helper()
	if validate == nil {
		return
	}
	if err := validate(value); err != nil {
		f.t.Fatalf("the answer %q for %q is not valid: %v", value, title, err)
	}
}

func (f *fakeUI) Input(title, _, value string, validate func(string) error) (string, error) {
	a := f.next("input", title)
	if a.value != nil {
		value = a.value.(string)
	}
	f.check(title, validate, value)
	return value, nil
}

func (f *fakeUI) Secret(title, _ string, validate func(string) error) (string, error) {
	a := f.next("secret", title)
	var value string
	switch v := a.value.(type) {
	case string:
		value = v
	case func() string:
		value = v()
	}
	f.check(title, validate, value)
	return value, nil
}

func (f *fakeUI) Select(title, _ string, opts []Option, value string) (string, error) {
	a := f.next("select", title)
	if a.value != nil {
		value = a.value.(string)
	}
	for _, o := range opts {
		if o.Value == value {
			return value, nil
		}
	}
	f.t.Fatalf("the value %q is not an option for %q", value, title)
	return "", nil
}

func (f *fakeUI) MultiSelect(title, _ string, _ []Option, values []string, validate func([]string) error) ([]string, error) {
	a := f.next("multiselect", title)
	if a.value != nil {
		values = a.value.([]string)
	}
	if err := validate(values); err != nil {
		f.t.Fatalf("the answer %v for %q is not valid: %v", values, title, err)
	}
	return values, nil
}

func (f *fakeUI) Confirm(title, _ string, value bool) (bool, error) {
	a := f.next("confirm", title)
	if a.value != nil {
		value = a.value.(bool)
	}
	return value, nil
}

func (f *fakeUI) WaitEnter(text string) error {
	f.next("wait", text)
	return nil
}

func (f *fakeUI) Heading(text string) { fmt.Fprintf(&f.out, "## %s\n", text) }
func (f *fakeUI) Info(text string)    { fmt.Fprintf(&f.out, "%s\n", text) }
func (f *fakeUI) Success(text string) { fmt.Fprintf(&f.out, "OK: %s\n", text) }
func (f *fakeUI) Warn(text string)    { fmt.Fprintf(&f.out, "WARNING: %s\n", text) }
func (f *fakeUI) Error(text string)   { fmt.Fprintf(&f.out, "ERROR: %s\n", text) }
func (f *fakeUI) Code(text string)    { fmt.Fprintf(&f.out, "```\n%s\n```\n", text) }

func (f *fakeUI) done() {
	f.t.Helper()
	if len(f.answers) > 0 {
		f.t.Fatalf("%d answers are not used, next %q\noutput:\n%s", len(f.answers), f.answers[0].match, f.out.String())
	}
}

type fakeEngine struct {
	probeErrors  []error
	server       engine.ServerInfo
	client       engine.ClientInfo
	databases    []string
	privilegeBad map[string]int
	schemaBad    map[string]int
}

func (f *fakeEngine) Name() string          { return "fake" }
func (f *fakeEngine) FileExtension() string { return "dump" }
func (f *fakeEngine) Probe(context.Context, string) (engine.ServerInfo, error) {
	return f.server, nil
}
func (f *fakeEngine) DumpCommand(context.Context, string) (*engine.Dump, error) {
	return nil, errors.New("not used")
}
func (f *fakeEngine) WrapOutput(w io.Writer) (io.WriteCloser, error) {
	return engine.NopWriteCloser(w), nil
}

func (f *fakeEngine) ProbeServer(context.Context) (engine.ServerInfo, error) {
	if len(f.probeErrors) > 0 {
		err := f.probeErrors[0]
		f.probeErrors = f.probeErrors[1:]
		return engine.ServerInfo{}, err
	}
	return f.server, nil
}

func (f *fakeEngine) ListDatabases(context.Context) ([]string, error) {
	return f.databases, nil
}

func (f *fakeEngine) ClientVersion(context.Context) (engine.ClientInfo, error) {
	return f.client, nil
}

func (f *fakeEngine) SchemaCheck(_ context.Context, db string) error {
	if f.schemaBad[db] > 0 {
		f.schemaBad[db]--
		return errors.New("pg_dump stopped with exit code 1: permission denied for table audit_log")
	}
	return nil
}

func (f *fakeEngine) PrivilegeCheck(_ context.Context, db string) (engine.PrivilegeReport, error) {
	if f.privilegeBad[db] > 0 {
		f.privilegeBad[db]--
		return engine.PrivilegeReport{Problems: []string{`"public"."audit_log"`}, FixSQL: "GRANT SELECT ON ALL TABLES IN SCHEMA \"public\" TO \"backup_ro\";\n"}, nil
	}
	return engine.PrivilegeReport{}, nil
}
