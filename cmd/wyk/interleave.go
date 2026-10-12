package main

import (
	"flag"
	"fmt"
	"strings"
)

// parseInterleaved parses args against fs, accepting flags and
// positionals in any order, and returns the positionals in the order
// they appeared.
//
// Go's flag package stops at the first non-flag argument, so
// `wyk handoff <id> -file x` left `-file x` in fs.Args(), the arity
// check failed, and the user saw bare usage with no explanation
// (would-you-kindly-y6hv, filed from a consumer as
// watch-ads-not-working-ief). Every subcommand that takes a positional
// goes through here instead so `wyk handoff <id> -file x` and
// `wyk handoff -file x <id>` are the same command.
//
// Semantics match flag.FlagSet.Parse otherwise: `--` ends flag parsing
// and everything after it is positional; a lone `-` is positional; a
// non-boolean flag consumes the next token as its value even when that
// token is flag-shaped (`-note "-x"`), so a value is never mistaken for
// a flag. Flags are handed to fs.Parse one at a time, so unknown flags,
// missing values and -h/-help produce the flag package's own errors and
// its usage output exactly as before; map the error with flagParseExit.
//
// Callers must read positionals from the returned slice, not fs.Args():
// after the loop the FlagSet only remembers the last token it saw.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		a := args[0]
		if a == "--" {
			return append(pos, args[1:]...), nil
		}
		if len(a) < 2 || a[0] != '-' {
			pos = append(pos, a)
			args = args[1:]
			continue
		}
		// Flag-shaped. Work out how many tokens this one flag consumes
		// so fs.Parse never sees the positional that may follow it.
		n := 1
		name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if base, _, hasValue := strings.Cut(name, "="); !hasValue && len(args) > 1 {
			if f := fs.Lookup(base); f != nil && !isBoolFlag(f) {
				n = 2
			}
		}
		if err := fs.Parse(args[:n]); err != nil {
			return nil, err
		}
		args = args[n:]
	}
	return pos, nil
}

// isBoolFlag reports whether f takes no value — the same test the flag
// package applies, via the optional IsBoolFlag method on flag.Value.
func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// unexpectedArg formats the error for a positional that no slot
// accepts: `wyk handoff: unexpected argument "-file" after <issue-id>`.
// after names the slot that was already filled, in the usage line's
// own spelling, so the message says both what was rejected and why.
func unexpectedArg(cmd, arg, after string) string {
	return fmt.Sprintf("wyk %s: unexpected argument %q after %s", cmd, arg, after)
}
