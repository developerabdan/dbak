package onboard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

type PlainUI struct {
	in     *bufio.Reader
	out    io.Writer
	secret func(title string) (string, error)
	echo   bool
}

func NewPlainUI(in io.Reader, out io.Writer, secret func(title string) (string, error)) *PlainUI {
	return &PlainUI{in: bufio.NewReader(in), out: out, secret: secret}
}

func (p *PlainUI) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", ErrAborted
		}
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if p.echo {
		fmt.Fprintln(p.out, line)
	}
	return line, nil
}

func (p *PlainUI) header(title, description string) {
	fmt.Fprintln(p.out)
	fmt.Fprintln(p.out, title)
	if description != "" {
		fmt.Fprintln(p.out, description)
	}
}

func (p *PlainUI) Input(title, description, value string, validate func(string) error) (string, error) {
	p.header(title, description)
	for {
		if value != "" {
			fmt.Fprintf(p.out, "Type a value, or push Enter for %q: ", value)
		} else {
			fmt.Fprint(p.out, "Type a value: ")
		}
		line, err := p.readLine()
		if err != nil {
			return "", err
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			answer = value
		}
		if validate != nil {
			if err := validate(answer); err != nil {
				fmt.Fprintf(p.out, "The value is not valid: %s.\n", err)
				continue
			}
		}
		return answer, nil
	}
}

func (p *PlainUI) Secret(title, description string, validate func(string) error) (string, error) {
	p.header(title, description)
	for {
		var answer string
		var err error
		if p.secret != nil {
			answer, err = p.secret("Type the value. The screen does not show it: ")
		} else {
			fmt.Fprint(p.out, "Type the value: ")
			echo := p.echo
			p.echo = false
			answer, err = p.readLine()
			p.echo = echo
			if echo {
				fmt.Fprintln(p.out)
			}
		}
		if err != nil {
			return "", err
		}
		if validate != nil {
			if err := validate(answer); err != nil {
				fmt.Fprintf(p.out, "The value is not valid: %s.\n", err)
				continue
			}
		}
		return answer, nil
	}
}

func (p *PlainUI) printOptions(opts []Option, marked func(Option) bool) {
	for i, o := range opts {
		mark := " "
		if marked(o) {
			mark = "*"
		}
		fmt.Fprintf(p.out, "%s %d. %s\n", mark, i+1, o.Label)
	}
}

func (p *PlainUI) Select(title, description string, opts []Option, value string) (string, error) {
	p.header(title, description)
	p.printOptions(opts, func(o Option) bool { return o.Value == value })
	for {
		fmt.Fprintf(p.out, "Type a number from 1 to %d, or push Enter for the option with *: ", len(opts))
		line, err := p.readLine()
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" && slices.ContainsFunc(opts, func(o Option) bool { return o.Value == value }) {
			return value, nil
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(opts) {
			fmt.Fprintln(p.out, "The number is not valid.")
			continue
		}
		return opts[n-1].Value, nil
	}
}

func (p *PlainUI) MultiSelect(title, description string, opts []Option, values []string, validate func([]string) error) ([]string, error) {
	p.header(title, description)
	p.printOptions(opts, func(o Option) bool { return slices.Contains(values, o.Value) })
	for {
		fmt.Fprint(p.out, "Type the numbers with a comma between them, or push Enter for the options with *: ")
		line, err := p.readLine()
		if err != nil {
			return nil, err
		}
		chosen, err := parseNumbers(line, opts, values)
		if err != nil {
			fmt.Fprintf(p.out, "%s.\n", err)
			continue
		}
		if validate != nil {
			if err := validate(chosen); err != nil {
				fmt.Fprintf(p.out, "The selection is not valid: %s.\n", err)
				continue
			}
		}
		return chosen, nil
	}
}

func parseNumbers(line string, opts []Option, current []string) ([]string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return current, nil
	}
	var chosen []string
	for _, field := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(field)
		if err != nil || n < 1 || n > len(opts) {
			return nil, fmt.Errorf("the number %q is not valid", field)
		}
		if !slices.Contains(chosen, opts[n-1].Value) {
			chosen = append(chosen, opts[n-1].Value)
		}
	}
	return chosen, nil
}

func (p *PlainUI) Confirm(title, description string, value bool) (bool, error) {
	p.header(title, description)
	hint := "y/N"
	if value {
		hint = "Y/n"
	}
	for {
		fmt.Fprintf(p.out, "Type y or n (%s): ", hint)
		line, err := p.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return value, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.out, "Type y or n.")
	}
}

func (p *PlainUI) WaitEnter(text string) error {
	fmt.Fprintln(p.out)
	fmt.Fprint(p.out, text+" ")
	_, err := p.readLine()
	return err
}

func (p *PlainUI) Heading(text string) {
	fmt.Fprintf(p.out, "\n== %s ==\n", text)
}

func (p *PlainUI) Info(text string) {
	fmt.Fprintln(p.out, text)
}

func (p *PlainUI) Success(text string) {
	fmt.Fprintln(p.out, "OK: "+text)
}

func (p *PlainUI) Warn(text string) {
	fmt.Fprintln(p.out, "WARNING: "+text)
}

func (p *PlainUI) Error(text string) {
	fmt.Fprintln(p.out, "ERROR: "+text)
}

func (p *PlainUI) Code(text string) {
	fmt.Fprintln(p.out)
	fmt.Fprintln(p.out, strings.TrimRight(text, "\n"))
	fmt.Fprintln(p.out)
}
