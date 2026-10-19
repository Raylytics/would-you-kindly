package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jimbottle/would-you-kindly/internal/beads"
)

type stubClaimer struct {
	res   ClaimResult
	err   error
	calls []string
}

func (c *stubClaimer) Claim(_ context.Context, i beads.Issue) (ClaimResult, error) {
	c.calls = append(c.calls, i.ID)
	return c.res, c.err
}

func claimModel(t *testing.T, c Claimer) (Model, *stubMutator) {
	t.Helper()
	s := &stubMutator{stubSource: stubSource{issues: sampleIssues()}}
	m := applyMutatorFetched(New(s), s).WithClaimer(c)
	return m, s
}

func pressX(m Model) (Model, tea.Cmd) {
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	return model.(Model), cmd
}

// x claims the cursor row through the Claimer, then the banner names
// the owner and the refetch runs (would-you-kindly-p00h.1).
func TestClaim_KeyClaimsCursorRow(t *testing.T) {
	until := time.Now().Add(2 * time.Hour)
	c := &stubClaimer{res: ClaimResult{Action: "claimed", Owner: "claude-abc12345", Until: until}}
	m, _ := claimModel(t, c)
	target := m.visible[m.cursor]

	m, cmd := pressX(m)
	if cmd == nil {
		t.Fatal("x should dispatch a claim")
	}
	if !strings.Contains(m.status, "claiming "+target.ID) {
		t.Errorf("status while in flight = %q", m.status)
	}
	msg, ok := cmd().(claimMsg)
	if !ok || msg.err != nil || msg.issue.ID != target.ID {
		t.Fatalf("claim cmd produced %+v", msg)
	}
	if len(c.calls) != 1 || c.calls[0] != target.ID {
		t.Errorf("Claimer calls = %v, want [%s]", c.calls, target.ID)
	}
	model, cmd := m.Update(msg)
	m = model.(Model)
	want := "claimed " + target.ID + " as claude-abc12345 until " + until.Local().Format("15:04")
	if m.status != want {
		t.Errorf("status = %q, want %q", m.status, want)
	}
	if cmd == nil {
		t.Error("a landed claim should refetch so the Owner column updates")
	}
	if m.lastAction.kind != "claim" {
		t.Errorf("lastAction = %+v, want claim (for .)", m.lastAction)
	}
}

func TestClaim_RenewAndTakeoverBanners(t *testing.T) {
	m, _ := claimModel(t, &stubClaimer{})
	i := m.visible[0]
	model, _ := m.Update(claimMsg{issue: i, res: ClaimResult{Action: "renewed", Owner: "me"}})
	if got := model.(Model).status; got != "renewed "+i.ID+" for me" {
		t.Errorf("renew banner = %q", got)
	}
	model, _ = m.Update(claimMsg{issue: i, res: ClaimResult{Action: "took-over", Owner: "me", Previous: "claude-old"}})
	if got := model.(Model).status; got != "took over "+i.ID+" from claude-old as me" {
		t.Errorf("takeover banner = %q", got)
	}
}

// A refusal (live lease elsewhere, or a fenced human / agent-handoff
// row) is the Claimer's error, shown sticky with its text.
func TestClaim_RefusalIsSticky(t *testing.T) {
	c := &stubClaimer{err: errors.New("a-1 is held by claude-zz (1h50m left); pick something else, or -force to take it over")}
	m, _ := claimModel(t, c)
	m, cmd := pressX(m)
	model, cmd := m.Update(cmd())
	m = model.(Model)
	if !strings.Contains(m.status, "claim a-1 failed: a-1 is held by claude-zz") {
		t.Errorf("status = %q", m.status)
	}
	if cmd != nil {
		t.Error("a refusal must stay on screen (no flash-clear, no refetch)")
	}
}

func TestClaim_NoClaimerExplains(t *testing.T) {
	s := &stubMutator{stubSource: stubSource{issues: sampleIssues()}}
	m := applyMutatorFetched(New(s), s)
	m, _ = pressX(m)
	if !strings.Contains(m.status, "wyk claim <id>") {
		t.Errorf("status = %q, want the CLI fallback hint", m.status)
	}
}

func TestClaim_ReadOnlySourceRefuses(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src), src).WithClaimer(&stubClaimer{})
	m, cmd := pressX(m)
	if cmd != nil || !strings.Contains(m.status, "read-only") {
		t.Errorf("read-only source: cmd=%v status=%q", cmd != nil, m.status)
	}
}

func TestClaim_ClosedRowRefusedBeforeDispatch(t *testing.T) {
	c := &stubClaimer{}
	m, _ := claimModel(t, c)
	m.visible[m.cursor].Status = "closed"
	m, cmd := pressX(m)
	if len(c.calls) != 0 || !strings.Contains(m.status, "is closed") {
		t.Errorf("closed row: calls=%v status=%q", c.calls, m.status)
	}
	if cmd == nil {
		t.Error("the hint should flash-clear")
	}
}

// Marked rows claim in bulk through the shared bulk-write path.
func TestClaim_BulkClaimsMarkedRows(t *testing.T) {
	c := &stubClaimer{res: ClaimResult{Action: "claimed", Owner: "me"}}
	m, _ := claimModel(t, c)
	m.marked = map[string]bool{issueKey(m.visible[0]): true, issueKey(m.visible[1]): true}
	m, cmd := pressX(m)
	bm, ok := cmd().(bulkWriteMsg)
	if !ok || bm.action != "claim" || bm.total != 2 || len(bm.failed) != 0 {
		t.Fatalf("bulk claim msg = %+v", bm)
	}
	if len(c.calls) != 2 {
		t.Errorf("Claimer calls = %v, want both rows", c.calls)
	}
	model, _ := m.Update(bm)
	if got := model.(Model).status; got != "claimed 2 rows" {
		t.Errorf("bulk banner = %q", got)
	}
}

// `.` redoes the last claim against the cursor row.
func TestClaim_RepeatRedispatches(t *testing.T) {
	c := &stubClaimer{res: ClaimResult{Action: "claimed", Owner: "me"}}
	m, _ := claimModel(t, c)
	m, cmd := pressX(m)
	_ = cmd()
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = model.(Model)
	model, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'.'}})
	m = model.(Model)
	if cmd == nil {
		t.Fatal(". should redo the claim")
	}
	msg := cmd().(claimMsg)
	if msg.issue.ID != m.visible[m.cursor].ID || len(c.calls) != 2 {
		t.Errorf("repeat claimed %s (calls %v), want the cursor row", msg.issue.ID, c.calls)
	}
}
