package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/jimbottle/would-you-kindly/internal/beads"
)

// closeRefRE matches lines of a commit message that signal an
// auto-close. The convention follows what most issue trackers
// recognise — Closes / Fixes / Resolves, case-insensitive, optionally
// suffixed with ":" or "#", followed by an ID that looks like a bd
// issue (lowercase + digits + hyphens, with optional ".N" suffixes
// for hierarchical IDs like would-you-kindly-ma5.4).
//
// Anchoring to line-start (multiline mode) keeps a stray "closes:"
// inside a code block or sentence from triggering a real close. The
// trailing `\s*$` anchor is intentional: it enforces ONE ID PER LINE.
// A trailer like "Closes: bd-1, bd-2" matches nothing — to close
// both, use two separate Closes lines. This avoids false positives
// from prose like "Closes: bd-1 (we'll handle bd-2 next week)" where
// the second token isn't really a close target.
var closeRefRE = regexp.MustCompile(`(?im)^[\s>]*(?:closes|fixes|resolves)[:\s#]+([a-z][a-z0-9-]*(?:\.[a-z0-9-]+)*)\s*$`)

// parseCloseRefs returns the issue IDs the commit message asks the
// hook to auto-close, in the order they appear, with duplicates
// removed. A purely lexical scan — no validation against bd happens
// here.
func parseCloseRefs(commitMessage string) []string {
	matches := closeRefRE.FindAllStringSubmatch(commitMessage, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var out []string
	for _, m := range matches {
		id := strings.ToLower(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// runHook is the top-level dispatcher for `wyk hook <subcommand>`:
// the git post-commit hook, the Claude Code PreToolUse/Stop hooks, and
// `dispatch`, which (re)sends one handoff-hook event for an issue
// (cmd/wyk/handoffhook.go).
func runHook(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: wyk hook <post-commit|bd-create-guard|agent-nudge|install-nudge|dispatch> [args]")
		return 64
	}
	switch args[0] {
	case "post-commit":
		return runHookPostCommit(args[1:])
	case "bd-create-guard":
		return runHookBDCreateGuard(os.Stdin)
	case "agent-nudge":
		return runHookAgentNudge(os.Stdin)
	case "install-nudge":
		return runHookInstallNudge(args[1:])
	case "dispatch":
		return runHookDispatch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "wyk hook: unknown subcommand %q\n", args[0])
		return 64
	}
}

// bdCreateRE matches an invocation of `bd create` at a command position
// — line start or right after a shell separator (newline, ; && || |, or
// an opening `(` / backtick command-substitution) — AND requires the
// `create` token to END at a token boundary: whitespace, end-of-string,
// a separator, or a redirect (`<`/`>`, e.g. `bd create>out`). So
// `bd create …`, `$(bd create …)`, and `bd create>out` are caught, while
// an arg like `echo "bd create"`, the `wyk create` wrapper, a hyphenated
// subcommand like `bd create-template`, and the concatenation
// `bd create"x"` (which is the token `createx`, a DIFFERENT subcommand,
// not a quoted arg to `create`) are not.
var bdCreateRE = regexp.MustCompile("(?:^|[\\n;&|(`])\\s*bd\\s+create(?:\\s|$|[;&|)`<>])")

// redactQuotes replaces every single- or double-quoted span in a shell
// command (the surrounding quote characters included) with a single NUL
// sentinel, so bdCreateRE never reads quoted TEXT as a command-position
// invocation. The motivating false positive: a multi-line
// `git commit -m "…\nbd create…\n…"` message, where a newline inside the
// quoted body looked like a shell separator. NUL is neither whitespace
// nor a separator nor a token boundary, so `bd create"x"` (shell token
// concatenation `createx`, a different subcommand) collapses to
// `bd create\x00` and stays allowed — matching the pre-existing
// behaviour. Backslash escapes are honoured at top level and inside
// double quotes; single quotes are literal (POSIX). An unterminated
// quote redacts to end-of-input — fail open, never block on malformed
// input, consistent with the rest of the guard.
//
// Heredoc bodies are redacted the same way (would-you-kindly-vxfz,
// would-you-kindly-2sgh): `<<TAG`, `<<-TAG`, `<<'TAG'` and `<<"TAG"`
// at top level register TAG, and at the next top-level newline each
// registered body — every line up to the one that equals TAG (leading
// tabs stripped for `<<-`) — collapses to the sentinel. A heredoc body
// is data (a commit message fed through `git commit -F -`, a script fed
// to `python3 -`, a doc edit mentioning the command in backticks), not
// a command position, and a backtick inside it used to anchor the
// regex as a command substitution. Several heredocs on one line are
// consumed in order; `<<<` (a here-string) is a redirect of a word and
// registers nothing; an unterminated heredoc redacts to end-of-input.
func redactQuotes(s string) string {
	var b strings.Builder
	r := []rune(s)
	// Heredoc tags whose bodies start at the next top-level newline,
	// in the order their `<<` operators appeared.
	var pending []heredocTag
	for i := 0; i < len(r); i++ {
		switch r[i] {
		case '\\':
			// Top-level escape: keep both runes so an escaped quote (\" or
			// \') can't open a quoted span. The escaped char itself is
			// written literally — harmless, because the regex only treats
			// UNescaped separators as command positions. (An escaped
			// separator like \; therefore stays a literal that bdCreateRE
			// may still match: a benign false-positive block, never a bypass.)
			b.WriteRune(r[i])
			if i+1 < len(r) {
				i++
				b.WriteRune(r[i])
			}
		case '\'': // single-quoted: literal until the next single quote
			b.WriteRune('\x00')
			for i++; i < len(r) && r[i] != '\''; i++ {
			}
		case '"': // double-quoted: backslash escapes the next rune
			b.WriteRune('\x00')
			for i++; i < len(r) && r[i] != '"'; i++ {
				if r[i] == '\\' && i+1 < len(r) {
					i++
				}
			}
		case '<':
			// `<<TAG` opens a heredoc; `<<<` is a here-string (a plain
			// redirect of one word) and `<` alone is a file redirect.
			if i+2 < len(r) && r[i+1] == '<' && r[i+2] == '<' {
				b.WriteString("<<<")
				i += 2
				continue
			}
			if i+1 < len(r) && r[i+1] == '<' {
				tag, next := parseHeredocTag(r, i+2)
				if tag.name != "" {
					pending = append(pending, tag)
					b.WriteString("<<")
					if tag.stripTabs {
						b.WriteRune('-')
					}
					b.WriteRune('\x00') // the tag word itself is not a command
					i = next - 1
					continue
				}
			}
			b.WriteRune(r[i])
		case '\n':
			b.WriteRune(r[i])
			if len(pending) == 0 {
				continue
			}
			// The bodies begin here, one after another, each ending at
			// its own terminator line. Everything between is data.
			i++
			for _, tag := range pending {
				i = skipHeredocBody(r, i, tag)
				b.WriteRune('\x00')
				b.WriteRune('\n')
			}
			pending = nil
			i-- // the loop increment lands on the rune after the last body
		default:
			b.WriteRune(r[i])
		}
	}
	return b.String()
}

// heredocTag is one registered `<<` operator: the terminator word and
// whether `<<-` was used (which lets the terminator carry leading tabs).
type heredocTag struct {
	name      string
	stripTabs bool
}

// parseHeredocTag reads the tag that follows a `<<` at r[i:]: an
// optional `-`, optional blanks, then a bare word, a single-quoted
// word, or a double-quoted word. It returns the tag and the index just
// past it; an empty name means "not a heredoc after all" (e.g. `<<`
// followed by nothing or by an operator), in which case the caller
// leaves the text alone.
func parseHeredocTag(r []rune, i int) (heredocTag, int) {
	var tag heredocTag
	if i < len(r) && r[i] == '-' {
		tag.stripTabs = true
		i++
	}
	for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
		i++
	}
	if i >= len(r) {
		return heredocTag{}, i
	}
	var name []rune
	switch r[i] {
	case '\'', '"':
		q := r[i]
		for i++; i < len(r) && r[i] != q; i++ {
			name = append(name, r[i])
		}
		if i < len(r) {
			i++ // closing quote
		}
	default:
		for i < len(r) && isHeredocWordRune(r[i]) {
			name = append(name, r[i])
			i++
		}
	}
	tag.name = string(name)
	return tag, i
}

// isHeredocWordRune bounds an unquoted heredoc tag: letters, digits and
// `_` cover every tag seen in practice (EOF, PYEOF, __END__), and
// stopping at anything else keeps `<<EOF;` or `<<EOF)` correct.
func isHeredocWordRune(c rune) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// skipHeredocBody returns the index just past the terminator line of
// the heredoc body that starts at r[i] (the first rune of the first
// body line). The terminator is a line whose content equals tag.name
// exactly, after leading tabs are stripped when `<<-` was used. An
// unterminated body runs to end-of-input.
func skipHeredocBody(r []rune, i int, tag heredocTag) int {
	for i < len(r) {
		end := i
		for end < len(r) && r[end] != '\n' {
			end++
		}
		line := r[i:end]
		if tag.stripTabs {
			for len(line) > 0 && line[0] == '\t' {
				line = line[1:]
			}
		}
		next := end
		if next < len(r) {
			next++ // consume the newline
		}
		if string(line) == tag.name {
			return next
		}
		i = next
	}
	return i
}

// allowBDCreateInlineRE matches `WYK_ALLOW_BD_CREATE=<value>` written
// into the command itself, as a prefix assignment or an export. The
// hook runs in Claude Code's environment, not the agent's shell, so a
// bypass set inline (the only way an agent can set it) must be read
// from the command text to count (would-you-kindly-2sgh).
var allowBDCreateInlineRE = regexp.MustCompile(`(?:^|[\s;&|(])(?:export\s+)?WYK_ALLOW_BD_CREATE=\S`)

// runHookBDCreateGuard is the Claude Code PreToolUse hook `wyk init`
// installs. It reads the tool-call JSON from stdin and, when an agent is
// about to run `bd create` in a Bash tool call, blocks it (exit 2) with a
// message telling the agent to use `wyk create` instead — which forwards
// the same flags to bd create AND stamps the Claude session so the TUI's
// Session column is populated. This makes the "use wyk create" convention
// enforced by the harness rather than a doc directive an agent can skip.
//
// `wyk create` itself shells out to `bd create` as a child process (not a
// Bash tool call), so the guard never sees it — no recursion. Set
// WYK_ALLOW_BD_CREATE=1 to bypass for a genuinely-needed raw bd create —
// in the hook's environment, or inline in the command text
// (`WYK_ALLOW_BD_CREATE=1 bd create …`), since an agent can only reach
// the latter.
// Anything it can't parse or doesn't recognise is allowed (exit 0): a
// guard that fails closed would wedge the agent on every Bash call.
func runHookBDCreateGuard(stdin io.Reader) int {
	if os.Getenv("WYK_ALLOW_BD_CREATE") != "" {
		return 0
	}
	var in struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return 0
	}
	if in.ToolName != "Bash" {
		return 0
	}
	redacted := redactQuotes(in.ToolInput.Command)
	if !bdCreateRE.MatchString(redacted) || allowBDCreateInlineRE.MatchString(redacted) {
		return 0
	}
	fmt.Fprintln(os.Stderr,
		"Use `wyk create` instead of `bd create`. It forwards the SAME flags to "+
			"`bd create` and also records the Claude session, so the issue shows up in "+
			"the TUI's Session column (traceable back to this conversation). Re-run the "+
			"command with `wyk create …`. "+
			"(If you genuinely need raw bd create, prefix the command with WYK_ALLOW_BD_CREATE=1.)")
	return 2 // PreToolUse exit 2 blocks the tool call; stderr is shown to Claude.
}

// runHookPostCommit is invoked by .git/hooks/post-commit (installed
// by `wyk init`). It reads the commit message from HEAD (or a
// supplied SHA, useful for testing), extracts auto-close references,
// and calls `bd close` on each.
//
// The function never returns non-zero on per-issue close failures —
// a post-commit hook running after the commit has already landed
// should never make the user's terminal look "failed". Each failure
// is printed and we move on.
//
// Exit codes:
//
//	0   success (or partial success — see above)
//	1   the commit-message read itself failed
//	2   bd missing or no workspace (rare — bd would have already failed
//	    the commit if it cared, but kept for parity with other modes)
//	64  usage error
func runHookPostCommit(args []string) int {
	fs := flag.NewFlagSet("hook post-commit", flag.ContinueOnError)
	fs.Usage = subcommandUsage(fs, "hook post-commit")
	dir := fs.String("C", "", "run as if bd had been started in this directory")
	fs.SetOutput(os.Stderr)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return flagParseExit(err)
	}

	ref := "HEAD"
	if len(pos) == 1 {
		ref = pos[0]
	} else if len(pos) > 1 {
		fmt.Fprintln(os.Stderr, unexpectedArg("hook post-commit", pos[1], "<commit-sha>"))
		return 64
	}

	// -C steers both the bd workspace AND the git repo we read the
	// commit message from. They must match — closing IDs from one
	// repo's git log against another repo's bd workspace is exactly
	// the kind of cross-talk the hook should never do.
	msg, err := commitMessage(*dir, ref)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wyk hook post-commit: read commit message:", err)
		return 1
	}

	ids := parseCloseRefs(msg)
	if len(ids) == 0 {
		// Silent on the "nothing to do" path so the post-commit hook
		// doesn't clutter normal commits.
		return 0
	}

	client := beads.NewClient()
	client.Dir = *dir

	for _, id := range ids {
		if err := client.Close(context.Background(), id); err != nil {
			switch {
			case errors.Is(err, beads.ErrBDNotFound):
				fmt.Fprintln(os.Stderr, "wyk hook: bd is not installed")
				return 2
			case errors.Is(err, beads.ErrNoWorkspace):
				fmt.Fprintln(os.Stderr, "wyk hook: no beads workspace here")
				return 2
			default:
				// Per-issue failures (already closed, unknown ID, …)
				// shouldn't fail the hook. bd's stderr is already in
				// the error text.
				fmt.Fprintf(os.Stderr, "wyk hook: close %s: %v\n", id, err)
				continue
			}
		}
		fmt.Printf("wyk hook: closed %s\n", id)
	}
	return 0
}

// commitMessage reads the full message of the given git ref. Uses
// `git show -s --format=%B` because it returns the body cleanly
// without needing to parse other show output. If dir is non-empty,
// git itself is invoked with -C <dir> so the read targets the same
// repo the bd client will write against.
func commitMessage(dir, ref string) (string, error) {
	args := []string{"show", "-s", "--format=%B", ref}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
