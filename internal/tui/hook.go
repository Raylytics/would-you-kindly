package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jimbottle/would-you-kindly/internal/beads"
)

// HookFirer is the optional seam through which the model tells an
// external task manager about a transition it just wrote (the handoff
// hook, docs/HOOKS.md): the `human` label added (handoff) or removed
// (bounce), or a human-labelled issue closed (close). main wires the
// configured hook here; nil means no hook and nothing is spawned
// (would-you-kindly-2pwr.5).
type HookFirer interface {
	// Wants reports whether a hook is configured for event, so the
	// model never spawns a command for an event nobody receives.
	Wants(event string) bool
	// Fire sends event for issue, after the bd write has landed, and
	// returns the hook's one-line answer for the status bar (a URL,
	// "ok", or "") or an error whose text the banner shows — the
	// implementation includes the replay command in it.
	Fire(ctx context.Context, event string, issue beads.Issue) (string, error)
}

// The hook events the TUI can cause. Strings rather than the hooks
// package's type so this package stays free of the CLI's wiring.
const (
	hookEventHandoff = "handoff"
	hookEventBounce  = "bounce"
	hookEventClose   = "close"
)

// hookMsg is the result of one hook batch: the event and the per-issue
// outcomes, successes and failures in dispatch order.
type hookMsg struct {
	event string
	lines []string // "<id>: <answer>" per success
	errs  []string // "<id>: <error>" per failure
}

// WithHandoffHook returns a copy of the model that fires h after the
// human-label transitions it writes. nil disables.
func (m Model) WithHandoffHook(h HookFirer) Model {
	m.hook = h
	return m
}

// hookEventFor maps a completed single write to the hook event it
// implies, or "" when the write is not a human-label transition.
// issue is the row as the model knew it BEFORE the write: for a close
// its labels say whether this was a human's task.
func hookEventFor(action string, issue beads.Issue) string {
	switch action {
	case "flag", "label:human":
		return hookEventHandoff
	case "unflag", "unlabel:human":
		return hookEventBounce
	case "close":
		if issue.IsHuman() {
			return hookEventClose
		}
	}
	return ""
}

// bulkHookTargets picks, from a bulk write's succeeded rows, the ones
// whose transition the hook should hear about, and the event. The
// bulk flag path counts an already-human row as succeeded without
// writing it, so those are left out; a bulk close reports only the
// human-labelled rows.
func bulkHookTargets(action string, succeeded []beads.Issue) (string, []beads.Issue) {
	var out []beads.Issue
	switch action {
	case "flag":
		for _, i := range succeeded {
			if !i.IsHuman() {
				out = append(out, i)
			}
		}
		return hookEventHandoff, out
	case "close":
		for _, i := range succeeded {
			if i.IsHuman() {
				out = append(out, i)
			}
		}
		return hookEventClose, out
	}
	return "", nil
}

// hookCmd returns the command that fires event for issues, or nil when
// there is no hook, no event, or nothing to send. One command runs the
// issues sequentially so a bulk flag reports once, in order, instead of
// racing the status line with N results.
func (m Model) hookCmd(event string, issues ...beads.Issue) tea.Cmd {
	if m.hook == nil || event == "" || len(issues) == 0 || !m.hook.Wants(event) {
		return nil
	}
	h := m.hook
	return func() tea.Msg {
		msg := hookMsg{event: event}
		for _, i := range issues {
			answer, err := h.Fire(context.Background(), event, i)
			if err != nil {
				msg.errs = append(msg.errs, i.ID+": "+err.Error())
				continue
			}
			if answer == "" {
				answer = "ok"
			}
			msg.lines = append(msg.lines, i.ID+": "+answer)
		}
		return msg
	}
}

// handleHookResult puts the hook's outcome on the status line. A
// success flashes like any write confirmation; a failure stays until
// the next key, like a write error — the text carries the replay
// command and the user needs time to read it. Either way bd is already
// written: the banner says so, so nobody retries the TUI action and
// makes a duplicate.
func (m Model) handleHookResult(msg hookMsg) (tea.Model, tea.Cmd) {
	switch {
	case len(msg.errs) == 0:
		if len(msg.lines) == 1 {
			m.setStatus("hook (" + msg.event + "): " + sanitizeInline(msg.lines[0]))
		} else {
			m.setStatus(fmt.Sprintf("hook (%s): %d mirrored", msg.event, len(msg.lines)))
		}
		return m, flashClearCmd(m.statusGen)
	case len(msg.lines) == 0 && len(msg.errs) == 1:
		m.setStatus("hook (" + msg.event + ") failed — bd is written, the mirror is not: " + sanitizeInline(msg.errs[0]))
	default:
		m.setStatus(fmt.Sprintf("hook (%s): %d ok, %d failed — bd is written, the mirror is not: %s",
			msg.event, len(msg.lines), len(msg.errs), sanitizeInline(msg.errs[0])))
	}
	return m, nil
}
