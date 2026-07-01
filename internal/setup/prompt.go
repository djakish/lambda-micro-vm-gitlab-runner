// Package setup implements the interactive `microvm-executor setup` wizard: a
// question-and-answer installer that provisions the AWS prerequisites, publishes
// the MicroVM image, and writes a ready-to-use GitLab Runner config.
package setup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ansi styling; disabled automatically when stdout is not a terminal.
type style struct {
	bold, dim, green, yellow, red, cyan, reset string
}

func newStyle(enabled bool) style {
	if !enabled {
		return style{}
	}
	return style{
		bold:   "\x1b[1m",
		dim:    "\x1b[2m",
		green:  "\x1b[32m",
		yellow: "\x1b[33m",
		red:    "\x1b[31m",
		cyan:   "\x1b[36m",
		reset:  "\x1b[0m",
	}
}

// prompter reads answers from the user with defaults and simple validation.
type prompter struct {
	in        *bufio.Reader
	out       io.Writer
	s         style
	assumeYes bool
}

func newPrompter(in io.Reader, out io.Writer, color, assumeYes bool) *prompter {
	return &prompter{in: bufio.NewReader(in), out: out, s: newStyle(color), assumeYes: assumeYes}
}

func (p *prompter) section(title string) {
	fmt.Fprintf(p.out, "\n%s%s%s\n", p.s.bold+p.s.cyan, title, p.s.reset)
	fmt.Fprintf(p.out, "%s%s%s\n", p.s.dim, strings.Repeat("─", len(title)), p.s.reset)
}

func (p *prompter) info(format string, a ...any) { fmt.Fprintf(p.out, "  "+format+"\n", a...) }
func (p *prompter) good(format string, a ...any) {
	fmt.Fprintf(p.out, "  %s✓%s "+format+"\n", append([]any{p.s.green, p.s.reset}, a...)...)
}
func (p *prompter) warn(format string, a ...any) {
	fmt.Fprintf(p.out, "  %s!%s "+format+"\n", append([]any{p.s.yellow, p.s.reset}, a...)...)
}
func (p *prompter) fail(format string, a ...any) {
	fmt.Fprintf(p.out, "  %s✗%s "+format+"\n", append([]any{p.s.red, p.s.reset}, a...)...)
}

// ask prompts with an optional default (shown in brackets); empty input takes it.
func (p *prompter) ask(label, def string) string {
	if def != "" {
		fmt.Fprintf(p.out, "  %s %s[%s]%s: ", label, p.s.dim, def, p.s.reset)
	} else {
		fmt.Fprintf(p.out, "  %s: ", label)
	}
	line, _ := p.in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// askRequired re-prompts until the user gives a non-empty value.
func (p *prompter) askRequired(label, def string) string {
	for {
		v := p.ask(label, def)
		if v != "" {
			return v
		}
		p.fail("this value is required")
	}
}

// askInt prompts for an integer, re-prompting on invalid input.
func (p *prompter) askInt(label string, def int) int {
	for {
		v := p.ask(label, strconv.Itoa(def))
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
		p.fail("please enter a number")
	}
}

// confirm asks a yes/no question. With --yes, defaults are taken silently.
func (p *prompter) confirm(label string, def bool) bool {
	if p.assumeYes {
		return def
	}
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for {
		fmt.Fprintf(p.out, "  %s %s(%s)%s: ", label, p.s.dim, hint, p.s.reset)
		line, _ := p.in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			p.fail("please answer y or n")
		}
	}
}

// secret reads a value without echoing it, using stty when a tty is available.
func (p *prompter) secret(label string) string {
	fmt.Fprintf(p.out, "  %s: ", label)
	restore := disableEcho()
	line, _ := p.in.ReadString('\n')
	restore()
	fmt.Fprintln(p.out)
	return strings.TrimSpace(line)
}

// disableEcho turns off terminal echo and returns a function to restore it.
// Best-effort: if stty is unavailable the input is simply visible.
func disableEcho() func() {
	if _, err := os.Stat("/dev/tty"); err != nil {
		return func() {}
	}
	set := func(arg string) {
		cmd := exec.Command("stty", arg)
		if tty, err := os.Open("/dev/tty"); err == nil {
			cmd.Stdin = tty
			_ = cmd.Run()
			_ = tty.Close()
		}
	}
	set("-echo")
	return func() { set("echo") }
}
