package onboard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

type TerminalUI struct {
	in  io.Reader
	out io.Writer

	heading lipgloss.Style
	success lipgloss.Style
	warn    lipgloss.Style
	fail    lipgloss.Style
}

func NewTerminalUI(in io.Reader, out io.Writer) *TerminalUI {
	renderer := lipgloss.NewRenderer(out)
	return &TerminalUI{
		in:      in,
		out:     out,
		heading: renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).MarginTop(1),
		success: renderer.NewStyle().Foreground(lipgloss.Color("10")),
		warn:    renderer.NewStyle().Foreground(lipgloss.Color("11")),
		fail:    renderer.NewStyle().Foreground(lipgloss.Color("9")),
	}
}

func NewStdUI() UI {
	terminal := isTerminal(os.Stdin)
	if terminal && os.Getenv("ACCESSIBLE") == "" {
		return NewTerminalUI(os.Stdin, os.Stdout)
	}
	var secret func(string) (string, error)
	if terminal {
		hidden := NewTerminalUI(os.Stdin, os.Stdout)
		secret = func(title string) (string, error) { return hidden.Secret(title, "", nil) }
	}
	ui := NewPlainUI(os.Stdin, os.Stdout, secret)
	ui.echo = !terminal
	return ui
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (u *TerminalUI) run(field huh.Field) error {
	err := huh.NewForm(huh.NewGroup(field)).WithInput(u.in).WithOutput(u.out).WithShowHelp(true).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

func validator(validate func(string) error) func(string) error {
	if validate == nil {
		return func(string) error { return nil }
	}
	return validate
}

func (u *TerminalUI) Input(title, description, value string, validate func(string) error) (string, error) {
	field := huh.NewInput().Title(title).Description(description).Value(&value).Validate(validator(validate))
	err := u.run(field)
	return strings.TrimSpace(value), err
}

func (u *TerminalUI) Secret(title, description string, validate func(string) error) (string, error) {
	var value string
	field := huh.NewInput().Title(title).Description(description).EchoMode(huh.EchoModePassword).Value(&value).Validate(validator(validate))
	err := u.run(field)
	return value, err
}

func (u *TerminalUI) Select(title, description string, opts []Option, value string) (string, error) {
	field := huh.NewSelect[string]().Title(title).Description(description).Options(huhOptions(opts, nil)...).Value(&value)
	err := u.run(field)
	return value, err
}

func (u *TerminalUI) MultiSelect(title, description string, opts []Option, values []string, validate func([]string) error) ([]string, error) {
	if validate == nil {
		validate = func([]string) error { return nil }
	}
	if description == "" {
		description = "Push the space bar to select or clear an item. Push Enter to continue."
	}
	field := huh.NewMultiSelect[string]().Title(title).Description(description).
		Options(huhOptions(opts, values)...).Value(&values).Validate(validate)
	err := u.run(field)
	return values, err
}

func huhOptions(opts []Option, selected []string) []huh.Option[string] {
	out := make([]huh.Option[string], 0, len(opts))
	for _, o := range opts {
		option := huh.NewOption(o.Label, o.Value)
		for _, s := range selected {
			if s == o.Value {
				option = option.Selected(true)
			}
		}
		out = append(out, option)
	}
	return out
}

func (u *TerminalUI) Confirm(title, description string, value bool) (bool, error) {
	field := huh.NewConfirm().Title(title).Description(description).Affirmative("Yes").Negative("No").Value(&value)
	err := u.run(field)
	return value, err
}

func (u *TerminalUI) WaitEnter(text string) error {
	return u.run(huh.NewNote().Title(text).Next(true).NextLabel("Continue"))
}

func (u *TerminalUI) Heading(text string) {
	fmt.Fprintln(u.out, u.heading.Render(text))
}

func (u *TerminalUI) Info(text string) {
	fmt.Fprintln(u.out, text)
}

func (u *TerminalUI) Success(text string) {
	fmt.Fprintln(u.out, u.success.Render("OK: "+text))
}

func (u *TerminalUI) Warn(text string) {
	fmt.Fprintln(u.out, u.warn.Render("WARNING: "+text))
}

func (u *TerminalUI) Error(text string) {
	fmt.Fprintln(u.out, u.fail.Render("ERROR: "+text))
}

func (u *TerminalUI) Code(text string) {
	fmt.Fprintln(u.out)
	fmt.Fprintln(u.out, strings.TrimRight(text, "\n"))
	fmt.Fprintln(u.out)
}
