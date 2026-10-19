package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jimbottle/would-you-kindly/internal/beads"
)

// Claimer checks an issue out as an expiring lease for the TUI's
// identity (wyk-contract/v4) — the same state machine `wyk claim`
// runs: claim when free, renew when already mine, take over an expired
// lease, refuse a live one held by someone else and refuse `human` /
// `agent-handoff` rows (the CLI's -force is the way past a fence).
// Optional; main wires it, and a nil Claimer makes `x` explain itself
// (would-you-kindly-p00h.1).
type Claimer interface {
	Claim(ctx context.Context, issue beads.Issue) (ClaimResult, error)
}

// ClaimResult is what a Claimer reports on success.
type ClaimResult struct {
	// Action is "claimed", "renewed" or "took-over".
	Action string
	Owner  string
	Until  time.Time
	// Previous is the holder a takeover displaced, when any.
	Previous string
}

// claimMsg carries one claim's outcome back to the model.
type claimMsg struct {
	issue beads.Issue
	res   ClaimResult
	err   error
}

// WithClaimer returns a copy of the model that can check issues out.
func (m Model) WithClaimer(c Claimer) Model {
	m.claimer = c
	return m
}

// handleClaim is the `x` key: check the cursor row out (or every marked
// row, bulk) as a lease. No confirmation — a claim is reversible with
// `wyk claim -release`, and a wrong one only costs a note on the issue.
func (m Model) handleClaim() (tea.Model, tea.Cmd) {
	if m.mutator() == nil {
		m.setStatus("read-only mode (no Mutator wired up)")
		return m, nil
	}
	if m.claimer == nil {
		m.setStatus("claiming is not wired up here — use `wyk claim <id>`")
		return m, flashClearCmd(m.statusGen)
	}
	if len(m.visible) == 0 {
		return m, nil
	}
	c := m.claimer
	if len(m.marked) > 0 {
		targets := m.markedIssues()
		m.marked = nil
		m.setStatus(fmt.Sprintf("claiming %d rows…", len(targets)))
		return m, runBulkWrite("claim", targets, func(ctx context.Context, i beads.Issue) error {
			_, err := c.Claim(ctx, i)
			return err
		})
	}
	i := m.visible[m.cursor]
	if i.Status == "closed" {
		m.setStatus(i.ID + " is closed; reopen it (u) before claiming")
		return m, flashClearCmd(m.statusGen)
	}
	m.lastAction = repeatableAction{kind: "claim"}
	m.setStatus("claiming " + i.ID + "…")
	return m, runClaim(c, i)
}

// runClaim wraps one Claimer call in a tea.Cmd.
func runClaim(c Claimer, i beads.Issue) tea.Cmd {
	return func() tea.Msg {
		res, err := c.Claim(context.Background(), i)
		return claimMsg{issue: i, res: res, err: err}
	}
}

// handleClaimResult reports the outcome and refetches so the Owner
// column shows the new @owner badge. A refusal is sticky, like every
// write error: it names the holder or the fence and the user needs to
// read it before the next key wipes it.
func (m Model) handleClaimResult(msg claimMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.setStatus("claim " + msg.issue.ID + " failed: " + sanitizeInline(msg.err.Error()))
		return m, nil
	}
	until := ""
	if !msg.res.Until.IsZero() {
		until = " until " + msg.res.Until.Local().Format("15:04")
	}
	switch msg.res.Action {
	case "renewed":
		m.setStatus(fmt.Sprintf("renewed %s for %s%s", msg.issue.ID, sanitizeInline(msg.res.Owner), until))
	case "took-over":
		m.setStatus(fmt.Sprintf("took over %s from %s as %s%s", msg.issue.ID, sanitizeInline(msg.res.Previous), sanitizeInline(msg.res.Owner), until))
	default:
		m.setStatus(fmt.Sprintf("claimed %s as %s%s", msg.issue.ID, sanitizeInline(msg.res.Owner), until))
	}
	return m, tea.Batch(m.fetchCmd(), flashClearCmd(m.statusGen))
}
