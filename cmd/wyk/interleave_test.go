package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

// newInterleaveFS builds a FlagSet shaped like handoff's: one string
// flag, one bool flag, output discarded so a deliberate parse error
// does not spray usage into the test log.
func newInterleaveFS() (*flag.FlagSet, *string, *bool) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := fs.String("file", "", "")
	dry := fs.Bool("dry-run", false, "")
	return fs, file, dry
}

func TestParseInterleaved_FlagsInAnyPosition(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"before", []string{"-dry-run", "-file", "x.md", "id-1"}},
		{"after", []string{"id-1", "-dry-run", "-file", "x.md"}},
		{"around", []string{"-dry-run", "id-1", "-file", "x.md"}},
		{"around-reversed", []string{"-file", "x.md", "id-1", "-dry-run"}},
		{"equals-form", []string{"id-1", "-file=x.md", "-dry-run=true"}},
		{"double-dash-spelling", []string{"id-1", "--dry-run", "--file", "x.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, file, dry := newInterleaveFS()
			pos, err := parseInterleaved(fs, tc.args)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(pos) != 1 || pos[0] != "id-1" {
				t.Errorf("positionals = %q, want [id-1]", pos)
			}
			if *file != "x.md" {
				t.Errorf("-file = %q, want x.md", *file)
			}
			if !*dry {
				t.Error("-dry-run not set")
			}
		})
	}
}

func TestParseInterleaved_DoubleDashEndsFlagParsing(t *testing.T) {
	fs, file, dry := newInterleaveFS()
	pos, err := parseInterleaved(fs, []string{"-file", "x.md", "--", "id-1", "-dry-run", "--", "-file"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"id-1", "-dry-run", "--", "-file"}
	if strings.Join(pos, " ") != strings.Join(want, " ") {
		t.Errorf("positionals = %q, want %q", pos, want)
	}
	if *file != "x.md" {
		t.Errorf("-file before -- should still bind; got %q", *file)
	}
	if *dry {
		t.Error("-dry-run after -- must be positional, not parsed")
	}
}

func TestParseInterleaved_FlagShapedValueBindsToItsFlag(t *testing.T) {
	// -note "-x" style: the token after a non-bool flag is its value
	// even when it starts with a dash, exactly as flag.Parse treats it.
	fs, file, dry := newInterleaveFS()
	pos, err := parseInterleaved(fs, []string{"id-1", "-file", "-x", "-dry-run"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *file != "-x" {
		t.Errorf("-file = %q, want -x", *file)
	}
	if !*dry {
		t.Error("-dry-run after the flag value should still parse")
	}
	if len(pos) != 1 || pos[0] != "id-1" {
		t.Errorf("positionals = %q, want [id-1]", pos)
	}
}

func TestParseInterleaved_BoolFlagDoesNotSwallowPositional(t *testing.T) {
	fs, _, dry := newInterleaveFS()
	pos, err := parseInterleaved(fs, []string{"-dry-run", "id-1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !*dry || len(pos) != 1 || pos[0] != "id-1" {
		t.Errorf("dry=%v positionals=%q; want dry=true [id-1]", *dry, pos)
	}
}

func TestParseInterleaved_LoneDashIsPositional(t *testing.T) {
	fs, _, _ := newInterleaveFS()
	pos, err := parseInterleaved(fs, []string{"-", "id-1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if strings.Join(pos, " ") != "- id-1" {
		t.Errorf("positionals = %q, want [- id-1]", pos)
	}
}

func TestParseInterleaved_ErrorsMatchFlagPackage(t *testing.T) {
	t.Run("unknown flag after positional", func(t *testing.T) {
		fs, _, _ := newInterleaveFS()
		_, err := parseInterleaved(fs, []string{"id-1", "-bogus"})
		if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -bogus") {
			t.Errorf("err = %v, want flag package's not-defined error", err)
		}
	})
	t.Run("missing value at end", func(t *testing.T) {
		fs, _, _ := newInterleaveFS()
		_, err := parseInterleaved(fs, []string{"id-1", "-file"})
		if err == nil || !strings.Contains(err.Error(), "flag needs an argument: -file") {
			t.Errorf("err = %v, want flag package's needs-an-argument error", err)
		}
	})
	t.Run("help after positional", func(t *testing.T) {
		fs, _, _ := newInterleaveFS()
		_, err := parseInterleaved(fs, []string{"id-1", "-h"})
		if !errors.Is(err, flag.ErrHelp) {
			t.Errorf("err = %v, want flag.ErrHelp so flagParseExit maps it to 0", err)
		}
	})
}

func TestParseInterleaved_NoArgs(t *testing.T) {
	fs, _, _ := newInterleaveFS()
	pos, err := parseInterleaved(fs, nil)
	if err != nil || len(pos) != 0 {
		t.Errorf("nil args: pos=%q err=%v, want empty, nil", pos, err)
	}
}

func TestUnexpectedArg(t *testing.T) {
	got := unexpectedArg("handoff", "-file", "<issue-id>")
	want := `wyk handoff: unexpected argument "-file" after <issue-id>`
	if got != want {
		t.Errorf("unexpectedArg = %q, want %q", got, want)
	}
}
