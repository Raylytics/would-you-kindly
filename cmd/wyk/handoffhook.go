package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jimbottle/would-you-kindly/internal/beads"
	"github.com/jimbottle/would-you-kindly/internal/hooks"
	"github.com/jimbottle/would-you-kindly/internal/registry"
	"github.com/jimbottle/would-you-kindly/internal/wykconfig"
)

// handoffHookEnvVar overrides hooks.handoff.command for one run — the
// same escape hatch WYK_DEFAULT_SCOPE is for default_scope. Useful
// for trying a script before committing it to config.json, and for
// a CI job that wants a hook the developer's machine doesn't have.
const handoffHookEnvVar = "WYK_HANDOFF_HOOK"

// exitHookFailed is `wyk handoff`'s exit code when the bd handoff
// LANDED but the handoff hook failed. Distinct from 1 (the handoff
// itself failed) so an agent can tell "retry the handoff" from
// "the human has the task; the external mirror is the only thing
// missing — replay it with wyk hook dispatch". Documented in cli.md.
const exitHookFailed = 3

// resolveHandoffHookConfig turns the persisted config (plus the env
// override) into the hooks.Config the dispatcher runs. A broken
// config file yields no hook rather than a crash: the handoff must
// still land, and `wyk doctor` reports the config problem.
func resolveHandoffHookConfig(cfg wykconfig.Config) hooks.Config {
	var out hooks.Config
	if h := cfg.Hooks.Handoff; h != nil {
		out.Command = h.Command
		out.Timeout = time.Duration(h.TimeoutSeconds) * time.Second
		for _, e := range h.Events {
			out.Events = append(out.Events, hooks.Event(e))
		}
	}
	if env := strings.TrimSpace(os.Getenv(handoffHookEnvVar)); env != "" {
		out.Command = env
	}
	return out
}

// newHandoffHookDispatcher is the seam tests replace: production
// resolves the config and shells out; tests hand back a Dispatcher
// with a recording Runner and whatever Config the test wants.
var newHandoffHookDispatcher = func() *hooks.Dispatcher {
	return &hooks.Dispatcher{Config: resolveHandoffHookConfig(loadConfigBestEffort())}
}

// hookIssueClient is the slice of beads.Client the hook path needs.
// An interface so the CLI wiring can be tested with a stub instead of
// a bd binary (the CLAUDE.md swappable-runner rule).
type hookIssueClient interface {
	Show(ctx context.Context, id string) (beads.Issue, error)
	SetExternalRef(ctx context.Context, id, ref string) error
	Note(ctx context.Context, id, text string) error
	IssuePrefix(ctx context.Context) (string, error)
}

// hookRepo describes the workspace for the payload. dir is the -C
// value ("" = cwd). The registry name wins when the workspace is
// registered (it is what the TUI's Repo column shows); the directory
// basename is the fallback. The bd issue prefix is asked for
// best-effort — a missing prefix must not block a handoff.
func hookRepo(ctx context.Context, client hookIssueClient, dir string) hooks.Repo {
	path := dir
	if path == "" {
		path, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	r := hooks.Repo{Name: filepath.Base(path), Path: path}
	if regPath, err := registry.DefaultPath(); err == nil {
		if reg, err := registry.Load(regPath); err == nil {
			if repo, ok := reg.RepoForDir(path); ok {
				r.Name = repo.Name
				r.Path = repo.Path
			}
		}
	}
	if client != nil {
		if prefix, err := client.IssuePrefix(ctx); err == nil {
			r.Prefix = prefix
		}
	}
	return r
}

// handoffHookInput is what a caller knows at the moment of the
// transition. Runbook is optional: when empty, the issue's current
// description (from bd show) is used, which is what bounce/close and
// the replay path want.
type handoffHookInput struct {
	Event    hooks.Event
	Actor    string
	ID       string
	Dir      string
	Runbook  string
	Identity string
	Note     string
	// Issue, when set, is used as-is and bd is not consulted. The replay
	// path (wyk hook dispatch) reads the issue first and refuses to run
	// on a failed read; the live handoff path leaves it nil and falls
	// back to a minimal issue, because there the bd write has already
	// landed and the hook is the only thing left to do.
	Issue *beads.Issue
}

// fireHandoffHook builds the payload for in, runs the hook, and
// records what it returned on the bd issue. It returns 0 when the
// hook is disabled, filtered, or succeeded, and exitHookFailed when
// it fired and failed. It never returns a bd-level failure code: by
// the time it runs, the transition that matters has already been
// written, and the caller's exit code should say so.
//
// Output discipline: progress and the url go to out (stdout, so an
// agent reading `wyk handoff` sees them in order); failures go to
// errw with the script's own stderr and the replay command.
func fireHandoffHook(ctx context.Context, client hookIssueClient, d *hooks.Dispatcher, in handoffHookInput, out, errw io.Writer) int {
	if d == nil || !d.Config.Wants(in.Event) {
		return 0
	}

	// Read the issue whole so the script sees labels, due_at, notes,
	// and the external_ref an earlier run recorded. A failed read is
	// not fatal — the hook still fires with what the caller knows —
	// but it is said out loud, because a script that never sees
	// external_ref will create a duplicate.
	var issue beads.Issue
	if in.Issue != nil {
		issue = *in.Issue
	} else if got, err := client.Show(ctx, in.ID); err != nil {
		fmt.Fprintf(errw, "wyk: hook: could not read %s for the payload (%v); sending a minimal issue\n", in.ID, err)
		issue = beads.Issue{ID: in.ID}
	} else {
		issue = got
	}
	runbook := in.Runbook
	if runbook == "" {
		runbook = issue.Description
	}
	p := hooks.Payload{
		Event:       in.Event,
		Actor:       in.Actor,
		WykVersion:  versionString(),
		Issue:       &issue,
		Runbook:     runbook,
		RunbookKind: hooks.ClassifyRunbook(runbook),
		Repo:        hookRepo(ctx, client, in.Dir),
		Identity:    in.Identity,
		Session:     strings.TrimSpace(os.Getenv(sessionEnvVar)),
		Note:        in.Note,
	}
	if issue.ExternalRef != "" {
		p.External = &hooks.External{Ref: issue.ExternalRef}
	}

	res, fired, err := d.Dispatch(ctx, p)
	if !fired {
		return 0
	}
	if err != nil {
		state := "bd is unaffected, the external mirror is missing"
		if errors.Is(err, hooks.ErrInterrupted) {
			// The group was killed, but a create the script had already
			// sent may have landed. Do not call the mirror missing: a
			// replay on that assumption is how a duplicate gets made.
			state = "bd is unaffected; the external mirror may or may not exist, check before replaying"
		}
		reportHookFailure(errw, in, err, state)
		return exitHookFailed
	}

	// Record the answer on the issue. external_ref is the idempotency
	// key for the next event; the note is for the human reading
	// `bd show` or the TUI detail pane. Either write failing is
	// reported as a hook failure — the external task exists but wyk
	// has lost the link to it, which is exactly the duplicate risk
	// the ref exists to prevent — with the replay command, since
	// replaying re-sends the (possibly absent) ref and lets the
	// script dedupe.
	if res.Ref != "" && res.Ref != issue.ExternalRef {
		if err := client.SetExternalRef(ctx, in.ID, res.Ref); err != nil {
			// The external task EXISTS; only the link to it was lost. Say
			// so, and give the one-liner that restores the link, because a
			// bare replay would resend without the ref and risk a duplicate.
			fmt.Fprintf(errw, "wyk: the handoff hook created the external task (ref %q) but recording that ref on %s failed: %v\n", res.Ref, in.ID, err)
			fmt.Fprintf(errw, "  Record it by hand: bd update %s --external-ref=%s --dolt-auto-commit=on\n", in.ID, res.Ref)
			fmt.Fprintf(errw, "  Then, if needed, replay with: %s\n", replayCommand(in))
			return exitHookFailed
		}
	}
	if res.URL != "" && in.Event == hooks.EventHandoff && (issue.ExternalRef == "" || res.Ref != issue.ExternalRef) {
		if err := client.Note(ctx, in.ID, "Handed off to "+res.URL); err != nil {
			fmt.Fprintf(errw, "wyk: hook: note failed (ref recorded, task exists): %v\n", err)
		}
	}
	switch {
	case res.URL != "":
		fmt.Fprintf(out, "hook (%s): %s\n", in.Event, res.URL)
	case res.Ref != "":
		fmt.Fprintf(out, "hook (%s): recorded external ref %s\n", in.Event, res.Ref)
	default:
		fmt.Fprintf(out, "hook (%s): ok\n", in.Event)
	}
	return 0
}

// reportHookFailure prints the failure in a shape an agent can act
// on: what failed, the script's own words, and the exact command
// that re-sends the same event once the cause is fixed.
//
// state is the parenthetical describing what the failure leaves behind,
// supplied by the caller because it differs by path: a hook that never
// answered leaves no mirror; one that answered but whose ref could not
// be recorded leaves a mirror wyk cannot find. Worded for both callers:
// after `wyk handoff` the bd write has already landed, after `wyk hook
// dispatch` there was none, and either way bd is as it was.
func reportHookFailure(errw io.Writer, in handoffHookInput, err error, state string) {
	fmt.Fprintf(errw, "wyk: the handoff hook failed for %s %s (%s): %v\n", in.Event, in.ID, state, err)
	if errors.Is(err, hooks.ErrTimedOut) {
		fmt.Fprintln(errw, "  Raise it with: wyk config set hooks.handoff.timeout_seconds <n>")
	}
	fmt.Fprintf(errw, "  Retry once fixed with: %s\n", replayCommand(in))
}

// replayCommand is the exact `wyk hook dispatch` invocation that re-sends
// in's event, with -C when the original ran against another directory.
func replayCommand(in handoffHookInput) string {
	if in.Dir != "" {
		return fmt.Sprintf("wyk hook dispatch -C %s %s %s", in.Dir, in.Event, in.ID)
	}
	return fmt.Sprintf("wyk hook dispatch %s %s", in.Event, in.ID)
}

// newHandoffHookClient is the bd seam for `wyk hook dispatch`: production
// shells out through beads.Client; tests substitute a stub so a failed
// `bd show` can be exercised without a bd binary.
var newHandoffHookClient = func(dir string) hookIssueClient {
	c := beads.NewClient()
	c.Dir = dir
	return c
}

// runHookDispatch implements `wyk hook dispatch <event> <id>`: rebuild
// the payload from bd and run the handoff hook for one issue. It is
// the replay path named in every hook failure, and the backfill path
// for issues handed off before the hook was configured.
//
// Exit codes: 0 fired (or no hook configured — said on stderr), 1 bd
// could not read the issue (nothing is sent), 2 bd missing / no
// workspace, 3 the hook fired and failed, 64 usage.
func runHookDispatch(args []string) int {
	fs := flag.NewFlagSet("hook dispatch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", "", "run as if bd had been started in this directory")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: wyk hook dispatch [-C <dir>] <handoff|bounce|close|ping> <issue-id>")
		fs.PrintDefaults()
	}
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return flagParseExit(err)
	}
	if len(pos) == 0 {
		fs.Usage()
		return 64
	}
	event := hooks.Event(pos[0])
	if !hooks.IsValidEvent(event) {
		fs.Usage()
		return 64
	}
	want := 2
	if event == hooks.EventPing {
		want = 1
	}
	switch {
	case len(pos) < want:
		fs.Usage()
		return 64
	case len(pos) > want:
		after := "<issue-id>"
		if want == 1 {
			after = "ping"
		}
		fmt.Fprintln(os.Stderr, unexpectedArg("hook dispatch", pos[want], after))
		return 64
	}
	id := ""
	if want == 2 {
		id = pos[1]
	}
	if *dir != "" {
		if err := validateDashC(*dir); err != nil {
			fmt.Fprintln(os.Stderr, "wyk hook dispatch:", err)
			return 64
		}
	}
	d := newHandoffHookDispatcher()
	if !d.Config.Enabled() {
		fmt.Fprintln(os.Stderr, "wyk hook dispatch: no handoff hook configured (wyk config set hooks.handoff.command …)")
		return 0
	}
	client := newHandoffHookClient(*dir)
	ctx := context.Background()
	if event == hooks.EventPing {
		p := hooks.Payload{Event: hooks.EventPing, Actor: hooks.ActorCLI, WykVersion: versionString(), Repo: hookRepo(ctx, client, *dir)}
		if _, _, err := d.Dispatch(ctx, p); err != nil {
			fmt.Fprintln(os.Stderr, "wyk hook dispatch: ping failed:", err)
			return exitHookFailed
		}
		fmt.Println("hook (ping): ok")
		return 0
	}
	// Replay has no bd write to protect, so it must not guess: a mistyped
	// id or a transient bd error would otherwise fire the hook with a
	// near-empty issue and no external_ref — the duplicate the ref exists
	// to prevent. Read first; refuse on failure.
	issue, err := client.Show(ctx, id)
	if err != nil {
		if code, msg, ok := classifyBDSentinel(err); ok {
			fmt.Fprintln(os.Stderr, msg)
			return code
		}
		fmt.Fprintf(os.Stderr, "wyk hook dispatch: cannot read %s from bd (nothing sent): %v\n", id, err)
		return 1
	}
	return fireHandoffHook(ctx, client, d, handoffHookInput{
		Event: event, Actor: hooks.ActorCLI, ID: id, Dir: *dir, Issue: &issue,
	}, os.Stdout, os.Stderr)
}

// tuiHookFirer adapts fireHandoffHook to the TUI's HookFirer seam
// (internal/tui/hook.go): the model names the event and the row, this
// finds the row's workspace and runs the same path `wyk handoff` does,
// with actor "tui" (would-you-kindly-2pwr.5). The issue is re-read from
// bd (Issue nil → fireHandoffHook's Show) so the script sees the row
// as it now is, not the pre-write snapshot the model held.
type tuiHookFirer struct {
	d *hooks.Dispatcher
	// dirs maps a registry name (Issue.Repo) to its path in multi-repo
	// mode; fallback is the single workspace otherwise ("" = cwd).
	dirs     map[string]string
	fallback string
	// newClient is newHandoffHookClient, swappable in tests.
	newClient func(dir string) hookIssueClient
}

// newTUIHookFirer builds the adapter for the workspaces the TUI shows.
// repoPaths is what buildSource returned: one path for a single
// workspace, else the registry's paths, in which case names are looked
// up from the registry so Issue.Repo can be routed.
func newTUIHookFirer(d *hooks.Dispatcher, repoPaths []string) *tuiHookFirer {
	f := &tuiHookFirer{d: d, newClient: newHandoffHookClient}
	if len(repoPaths) == 1 {
		f.fallback = repoPaths[0]
		return f
	}
	f.dirs = map[string]string{}
	if regPath, err := registry.DefaultPath(); err == nil {
		if reg, err := registry.Load(regPath); err == nil {
			for _, r := range reg.Repos {
				f.dirs[r.Name] = r.Path
			}
		}
	}
	return f
}

func (f *tuiHookFirer) Wants(event string) bool {
	return f.d != nil && f.d.Config.Wants(hooks.Event(event))
}

// dirFor resolves the workspace an issue lives in.
func (f *tuiHookFirer) dirFor(i beads.Issue) string {
	if p, ok := f.dirs[i.Repo]; ok {
		return p
	}
	return f.fallback
}

func (f *tuiHookFirer) Fire(ctx context.Context, event string, i beads.Issue) (string, error) {
	dir := f.dirFor(i)
	var out, errw bytes.Buffer
	code := fireHandoffHook(ctx, f.newClient(dir), f.d, handoffHookInput{
		Event: hooks.Event(event), Actor: hooks.ActorTUI, ID: i.ID, Dir: dir,
	}, &out, &errw)
	if code != 0 {
		return "", errors.New(oneLine(errw.String()))
	}
	return hookAnswer(out.String()), nil
}

// hookAnswer reduces fireHandoffHook's stdout ("hook (handoff): <url>")
// to the part after the event tag, for a status line that already
// names the event.
func hookAnswer(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if _, rest, ok := strings.Cut(last, "): "); ok && strings.HasPrefix(last, "hook (") {
		return rest
	}
	return last
}

// oneLine folds a multi-line stderr report into one banner line: the
// "wyk: " prefix dropped, lines joined with " · ".
func oneLine(stderr string) string {
	var parts []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "wyk: "))
		if l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " · ")
}
