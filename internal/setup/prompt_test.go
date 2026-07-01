package setup

import (
	"bytes"
	"strings"
	"testing"
)

func newTestPrompter(input string) (*prompter, *bytes.Buffer) {
	var out bytes.Buffer
	return newPrompter(strings.NewReader(input), &out, false, false), &out
}

func TestAskDefaultAndValue(t *testing.T) {
	p, _ := newTestPrompter("\nexplicit\n")
	if got := p.ask("x", "thedefault"); got != "thedefault" {
		t.Errorf("empty input: got %q, want default", got)
	}
	if got := p.ask("x", "thedefault"); got != "explicit" {
		t.Errorf("typed input: got %q, want explicit", got)
	}
}

func TestConfirm(t *testing.T) {
	cases := []struct {
		in   string
		def  bool
		want bool
	}{
		{"y\n", false, true},
		{"n\n", true, false},
		{"\n", true, true},
		{"\n", false, false},
		{"yes\n", false, true},
	}
	for _, c := range cases {
		p, _ := newTestPrompter(c.in)
		if got := p.confirm("ok?", c.def); got != c.want {
			t.Errorf("confirm(%q,def=%v) = %v, want %v", c.in, c.def, got, c.want)
		}
	}
}

func TestConfirmAssumeYes(t *testing.T) {
	p := newPrompter(strings.NewReader(""), &bytes.Buffer{}, false, true)
	if !p.confirm("proceed?", true) {
		t.Error("assumeYes should take the default (true)")
	}
	if p.confirm("proceed?", false) {
		t.Error("assumeYes should take the default (false)")
	}
}

func TestAskInt(t *testing.T) {
	p, _ := newTestPrompter("notanumber\n42\n")
	if got := p.askInt("n", 7); got != 42 {
		t.Errorf("askInt = %d, want 42 (after rejecting bad input)", got)
	}
	p2, _ := newTestPrompter("\n")
	if got := p2.askInt("n", 9); got != 9 {
		t.Errorf("askInt empty = %d, want default 9", got)
	}
}
