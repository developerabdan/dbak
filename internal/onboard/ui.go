package onboard

import "errors"

var ErrAborted = errors.New("dbak init stopped before the end. The config did not change")

type Option struct {
	Label string
	Value string
}

type UI interface {
	Input(title, description, value string, validate func(string) error) (string, error)
	Secret(title, description string, validate func(string) error) (string, error)
	Select(title, description string, options []Option, value string) (string, error)
	MultiSelect(title, description string, options []Option, values []string, validate func([]string) error) ([]string, error)
	Confirm(title, description string, value bool) (bool, error)
	WaitEnter(text string) error
	Heading(text string)
	Info(text string)
	Success(text string)
	Warn(text string)
	Error(text string)
	Code(text string)
}

func options(pairs ...string) []Option {
	out := make([]Option, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Option{Label: pairs[i], Value: pairs[i+1]})
	}
	return out
}

func sameOptions(values ...string) []Option {
	out := make([]Option, 0, len(values))
	for _, v := range values {
		out = append(out, Option{Label: v, Value: v})
	}
	return out
}
