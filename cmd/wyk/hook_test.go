package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestBDCreateGuard(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    int // 0 allow, 2 block
	}{
		{"bd create blocks", `{"tool_name":"Bash","tool_input":{"command":"bd create --title x"}}`, 2},
		{"chained bd create blocks", `{"tool_name":"Bash","tool_input":{"command":"cd x && bd create -t y"}}`, 2},
		{"subshell bd create blocks", `{"tool_name":"Bash","tool_input":{"command":"id=$(bd create -t y)"}}`, 2},
		{"backtick subshell bd create blocks", "{\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"id=`bd create -t y`\"}}", 2},
		{"bd create-template not matched", `{"tool_name":"Bash","tool_input":{"command":"bd create-template foo"}}`, 0},
		{"bd create with redirect blocks", `{"tool_name":"Bash","tool_input":{"command":"bd create>out.txt"}}`, 2},
		{"bd createX concatenation not matched", `{"tool_name":"Bash","tool_input":{"command":"bd create\"x\""}}`, 0},
		{"wyk create allowed", `{"tool_name":"Bash","tool_input":{"command":"wyk create --title x"}}`, 0},
		{"bd list allowed", `{"tool_name":"Bash","tool_input":{"command":"bd list --json"}}`, 0},
		{"bd create as arg allowed", `{"tool_name":"Bash","tool_input":{"command":"echo bd create"}}`, 0},
		{"bd createX not matched", `{"tool_name":"Bash","tool_input":{"command":"bd createfoo"}}`, 0},
		{"double-quoted bd create allowed", `{"tool_name":"Bash","tool_input":{"command":"echo \"bd create\""}}`, 0},
		{"single-quoted bd create allowed", `{"tool_name":"Bash","tool_input":{"command":"echo 'bd create'"}}`, 0},
		{"bd create in single-line commit message allowed", `{"tool_name":"Bash","tool_input":{"command":"git commit -m \"docs: prefer wyk create over bd create\""}}`, 0},
		{"bd create at start of wrapped commit body line allowed", `{"tool_name":"Bash","tool_input":{"command":"git commit -m \"chore: integrate wyk\n\n- routes raw\n  bd create through wyk\""}}`, 0},
		{"real bd create with quoted title mentioning bd create blocks", `{"tool_name":"Bash","tool_input":{"command":"bd create --title \"mentions bd create inside\""}}`, 2},
		// Command substitution nested INSIDE double quotes is an intentional
		// fail-open gap: the shell would execute it, but redactQuotes
		// collapses the whole double-quoted span, so the guard does NOT fire.
		// (The same forms at top level — see the $(...)/backtick cases above —
		// DO block.) Pinned so the trade-off is deliberate, not accidental.
		{"$() bd create inside double quotes intentionally allowed", `{"tool_name":"Bash","tool_input":{"command":"echo \"$(bd create -t y)\""}}`, 0},
		{"backtick bd create inside double quotes intentionally allowed", "{\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"echo \\\"`bd create -t y`\\\"\"}}", 0},
		// Heredoc bodies are data, not command positions
		// (would-you-kindly-vxfz, would-you-kindly-2sgh).
		{"commit message via heredoc mentioning bd create allowed", `{"tool_name":"Bash","tool_input":{"command":"git commit -F - <<EOF\nfeat: file issues\n\nbd create -f files several issues\nEOF"}}`, 0},
		{"quoted-tag heredoc with backtick bd create allowed", `{"tool_name":"Bash","tool_input":{"command":"python3 - <<'PYEOF'\ns = \"prefer ` + "`bd create`" + ` here\"\nPYEOF"}}`, 0},
		{"double-quoted-tag heredoc allowed", `{"tool_name":"Bash","tool_input":{"command":"cat <<\"EOF\" > notes.md\nbd create --title x\nEOF"}}`, 0},
		{"<<- heredoc with tab-indented terminator allowed", `{"tool_name":"Bash","tool_input":{"command":"cat <<-EOF\n\tbd create --title x\n\tEOF"}}`, 0},
		{"two heredocs on one line both redacted", `{"tool_name":"Bash","tool_input":{"command":"diff <(cat <<A\nbd create\nA\n) <(cat <<B\nbd create\nB\n)"}}`, 0},
		{"real bd create after a heredoc terminator still blocks", `{"tool_name":"Bash","tool_input":{"command":"cat <<EOF\nhello\nEOF\nbd create --title x"}}`, 2},
		{"real bd create on the heredoc's own line still blocks", `{"tool_name":"Bash","tool_input":{"command":"cat <<EOF && bd create --title x\nbody\nEOF"}}`, 2},
		{"unterminated heredoc fails open", `{"tool_name":"Bash","tool_input":{"command":"cat <<EOF\nbd create --title x\n"}}`, 0},
		{"here-string is not a heredoc: following bd create blocks", `{"tool_name":"Bash","tool_input":{"command":"cat <<< hello\nbd create --title x"}}`, 2},
		{"inline WYK_ALLOW_BD_CREATE=1 prefix allowed", `{"tool_name":"Bash","tool_input":{"command":"WYK_ALLOW_BD_CREATE=1 bd create --title x"}}`, 0},
		{"inline export WYK_ALLOW_BD_CREATE=1 allowed", `{"tool_name":"Bash","tool_input":{"command":"export WYK_ALLOW_BD_CREATE=1; bd create --title x"}}`, 0},
		{"WYK_ALLOW_BD_CREATE mentioned in a quoted string does not bypass", `{"tool_name":"Bash","tool_input":{"command":"echo \"WYK_ALLOW_BD_CREATE=1\"; bd create --title x"}}`, 2},
		{"non-Bash tool allowed", `{"tool_name":"Edit","tool_input":{"command":"bd create x"}}`, 0},
		{"malformed payload allowed", `not json`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runHookBDCreateGuard(strings.NewReader(c.payload)); got != c.want {
				t.Errorf("guard(%q) = %d, want %d", c.payload, got, c.want)
			}
		})
	}
}

// redactQuotes must leave the shape of the command intact outside the
// redacted spans, so the separator/anchor logic in bdCreateRE still sees
// real command positions.
func TestRedactQuotes_Heredoc(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cat <<EOF\nbody\nEOF\n", "cat <<\x00\n\x00\n"},
		{"cat <<EOF\nbody\nEOF\nnext", "cat <<\x00\n\x00\nnext"},
		{"cat <<-EOF\n\tbody\n\tEOF\nnext", "cat <<-\x00\n\x00\nnext"},
		{"cat <<'E'\nbody\nE\n", "cat <<\x00\n\x00\n"},
		{"cat <<<word\nnext", "cat <<<word\nnext"},
		{"a <<X; b <<Y\nx\nX\ny\nY\nz", "a <<\x00; b <<\x00\n\x00\n\x00\nz"},
		{"cat <<EOF\nnever ends", "cat <<\x00\n\x00\n"},
		{"x << ", "x << "},
	}
	for _, c := range cases {
		if got := redactQuotes(c.in); got != c.want {
			t.Errorf("redactQuotes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBDCreateGuard_BypassEnv(t *testing.T) {
	t.Setenv("WYK_ALLOW_BD_CREATE", "1")
	payload := `{"tool_name":"Bash","tool_input":{"command":"bd create --title x"}}`
	if got := runHookBDCreateGuard(strings.NewReader(payload)); got != 0 {
		t.Errorf("WYK_ALLOW_BD_CREATE=1 should bypass; got exit %d", got)
	}
}

func TestParseCloseRefs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single Closes trailer",
			in:   "Subject line\n\nBody.\n\nCloses: bd-42",
			want: []string{"bd-42"},
		},
		{
			name: "Fixes and Resolves both match",
			in:   "Subject\n\nFixes: bd-1\nResolves: bd-2\n",
			want: []string{"bd-1", "bd-2"},
		},
		{
			name: "case-insensitive keyword",
			in:   "CLOSES: bd-1\ncloses: bd-2\nFiXeS: bd-3",
			want: []string{"bd-1", "bd-2", "bd-3"},
		},
		{
			name: "# separator (github-style)",
			in:   "Closes #bd-42",
			want: []string{"bd-42"},
		},
		{
			name: "hierarchical IDs with dots",
			in:   "Closes: would-you-kindly-ma5.4\nFixes: my-proj-abc.1.2",
			want: []string{"would-you-kindly-ma5.4", "my-proj-abc.1.2"},
		},
		{
			name: "duplicates collapsed, order preserved",
			in:   "Closes: bd-1\nCloses: bd-2\nCloses: bd-1",
			want: []string{"bd-1", "bd-2"},
		},
		{
			name: "inline mention not at line start is ignored",
			in:   "We are not going to close it. closes: bd-99 in the middle of a sentence",
			want: nil,
		},
		{
			name: "code-block-like prefix tolerated (> for quotes only)",
			in:   "> Closes: bd-1",
			want: []string{"bd-1"},
		},
		{
			name: "no refs",
			in:   "Just a subject\n\nNo trailers here.",
			want: nil,
		},
		{
			name: "blank message",
			in:   "",
			want: nil,
		},
		{
			// One ID per line is the documented behaviour. A trailer
			// listing multiple IDs is rejected wholesale rather than
			// partially matched — see the closeRefRE comment for why.
			name: "comma-separated IDs on one line are NOT matched (use separate lines)",
			in:   "Subject\n\nCloses: bd-1, bd-2",
			want: nil,
		},
		{
			name: "two separate Closes lines both match",
			in:   "Subject\n\nCloses: bd-1\nCloses: bd-2",
			want: []string{"bd-1", "bd-2"},
		},
		{
			name: "trailing text after ID rejects the line",
			in:   "Closes: bd-1 (we'll handle bd-2 next week)",
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseCloseRefs(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseCloseRefs() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseCloseRefs_RealWykCommit(t *testing.T) {
	// A real-shaped message from this project. The actual commits we
	// shipped used "Refs: <id>" (no auto-close) and "Closes: <id>"
	// (explicit auto-close) — only the latter should fire.
	msg := `feat(beads): wire bd CLI client

internal/beads/client.go shells out to the bd binary.

Refs: would-you-kindly-8et
Closes: would-you-kindly-ci6`
	got := parseCloseRefs(msg)
	want := []string{"would-you-kindly-ci6"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// `wyk hook post-commit <sha> -C <dir>` must read -C rather than treat
// it as a second positional (would-you-kindly-y6hv). A bad -C then
// fails at the git read (exit 1), which proves the flag was bound.
func TestHookPostCommit_FlagsAfterSHA(t *testing.T) {
	var code int
	_, stderr := captureOutErr(t, func() { code = runHookPostCommit([]string{"HEAD", "-C", "/no/such/dir"}) })
	if code != 1 || !strings.Contains(stderr, "read commit message") {
		t.Errorf("-C after sha: exit %d, stderr %q; want 1 and a git read failure", code, stderr)
	}
	_, stderr = captureOutErr(t, func() { code = runHookPostCommit([]string{"HEAD", "extra"}) })
	if code != 64 || !strings.Contains(stderr, `wyk hook post-commit: unexpected argument "extra" after <commit-sha>`) {
		t.Errorf("extra positional: exit %d, stderr %q", code, stderr)
	}
}
