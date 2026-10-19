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

// stubFirer records what the model asked it to send.
type stubFirer struct {
	wants  bool
	answer string
	err    error
	calls  []string // "<event> <id>@<repo>"
}

func (f *stubFirer) Wants(string) bool { return f.wants }
func (f *stubFirer) Fire(_ context.Context, event string, i beads.Issue) (string, error) {
	f.calls = append(f.calls, event+" "+i.ID+"@"+i.Repo)
	return f.answer, f.err
}

// findHookMsg runs cmd (walking batches) and returns the hookMsg it
// produces, if any. Each leaf runs with a deadline so flashClearCmd's
// timer never stalls the test.
func findHookMsg(cmd tea.Cmd) (hookMsg, bool) {
	if cmd == nil {
		return hookMsg{}, false
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		switch v := msg.(type) {
		case hookMsg:
			return v, true
		case tea.BatchMsg:
			// Children run concurrently: a sibling flashClearCmd timer
			// must not delay finding the hook result.
			type res struct {
				hm hookMsg
				ok bool
			}
			results := make(chan res, len(v))
			for _, c := range v {
				go func(c tea.Cmd) {
					hm, ok := findHookMsg(c)
					results <- res{hm, ok}
				}(c)
			}
			for range v {
				if r := <-results; r.ok {
					return r.hm, true
				}
			}
		}
	case <-time.After(time.Second):
	}
	return hookMsg{}, false
}

func hookedModel(t *testing.T, f *stubFirer) (Model, *stubMutator) {
	t.Helper()
	s := &stubMutator{stubSource: stubSource{issues: sampleIssues()}}
	m := applyMutatorFetched(New(s), s).WithHandoffHook(f)
	return m, s
}

func TestHookEventFor(t *testing.T) {
	human := beads.Issue{ID: "h", Labels: []string{"human"}}
	agent := beads.Issue{ID: "a", Labels: []string{"src:agent"}}
	cases := []struct {
		action string
		issue  beads.Issue
		want   string
	}{
		{"flag", agent, hookEventHandoff},
		{"label:human", agent, hookEventHandoff},
		{"unflag", human, hookEventBounce},
		{"unlabel:human", human, hookEventBounce},
		{"close", human, hookEventClose},
		{"close", agent, ""},
		{"label:urgent", agent, ""},
		{"note", human, ""},
		{"reopen", human, ""},
	}
	for _, c := range cases {
		if got := hookEventFor(c.action, c.issue); got != c.want {
			t.Errorf("hookEventFor(%q, %s) = %q, want %q", c.action, c.issue.ID, got, c.want)
		}
	}
}

// H on an agent row: the write lands, then the hook hears "handoff"
// for that issue, and its answer reaches the status line.
func TestHook_FlagFiresHandoffAfterWrite(t *testing.T) {
	f := &stubFirer{wants: true, answer: "https://todo.example/t/1"}
	m, s := hookedModel(t, f)
	// Row 1 is the non-human sample issue.
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = model.(Model)
	target := m.visible[m.cursor]
	if target.IsHuman() {
		t.Fatalf("test setup: expected a non-human row under the cursor, got %+v", target)
	}
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	m = model.(Model)
	wm, ok := cmd().(writeMsg)
	if !ok || wm.action != "flag" || wm.issue.ID != target.ID {
		t.Fatalf("H should dispatch a flag write carrying the issue; got %+v", wm)
	}
	if len(f.calls) != 0 {
		t.Fatalf("hook fired before the bd write landed: %v", f.calls)
	}
	if len(s.added) != 1 {
		t.Fatalf("AddLabel not dispatched: %+v", s.added)
	}

	model, cmd = m.Update(wm)
	m = model.(Model)
	hm, ok := findHookMsg(cmd)
	if !ok {
		t.Fatal("write result did not schedule the hook")
	}
	if want := "handoff " + target.ID + "@" + target.Repo; len(f.calls) != 1 || f.calls[0] != want {
		t.Errorf("hook calls = %v, want [%s]", f.calls, want)
	}
	if hm.event != hookEventHandoff || len(hm.lines) != 1 || len(hm.errs) != 0 {
		t.Errorf("hookMsg = %+v", hm)
	}
	model, cmd = m.Update(hm)
	m = model.(Model)
	if !strings.Contains(m.status, "hook (handoff): "+target.ID+": https://todo.example/t/1") {
		t.Errorf("status = %q, want the hook's answer", m.status)
	}
	if cmd == nil {
		t.Error("a successful hook result should flash-clear like any confirmation")
	}
}

// H on a human row removes the label: the hook hears "bounce".
func TestHook_UnflagFiresBounce(t *testing.T) {
	f := &stubFirer{wants: true}
	m, _ := hookedModel(t, f)
	target := m.visible[m.cursor]
	if !target.IsHuman() {
		t.Fatalf("test setup: expected the human row under the cursor, got %+v", target)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	wm := cmd().(writeMsg)
	_, cmd = m.Update(wm)
	hm, ok := findHookMsg(cmd)
	if !ok || hm.event != hookEventBounce {
		t.Fatalf("expected a bounce hook, got ok=%v %+v", ok, hm)
	}
	if len(f.calls) != 1 || !strings.HasPrefix(f.calls[0], "bounce "+target.ID) {
		t.Errorf("hook calls = %v", f.calls)
	}
}

// Closing a human task completes its mirror; closing an agent task is
// nobody else's business.
func TestHook_CloseFiresOnlyForHumanIssue(t *testing.T) {
	f := &stubFirer{wants: true}
	m, _ := hookedModel(t, f)
	human := m.visible[0]
	_, cmd := m.Update(writeMsg{action: "close", id: human.ID, issue: human})
	if hm, ok := findHookMsg(cmd); !ok || hm.event != hookEventClose {
		t.Errorf("close of a human issue should fire close; got ok=%v %+v", ok, hm)
	}

	f.calls = nil
	agent := m.visible[1]
	_, cmd = m.Update(writeMsg{action: "close", id: agent.ID, issue: agent})
	if _, ok := findHookMsg(cmd); ok || len(f.calls) != 0 {
		t.Errorf("close of an agent issue must not reach the hook; calls=%v", f.calls)
	}
}

// Bulk H adds the label to every marked row that lacks it; the hook
// hears about exactly those rows, in one message.
func TestHook_BulkFlagFiresForNewlyFlaggedOnly(t *testing.T) {
	f := &stubFirer{wants: true}
	m, _ := hookedModel(t, f)
	m.marked = map[string]bool{}
	var agents int
	for _, i := range m.visible {
		m.marked[issueKey(i)] = true
		if !i.IsHuman() {
			agents++
		}
	}
	if agents == 0 || agents == len(m.visible) {
		t.Fatalf("test setup: want a mixed selection, got %d agent rows of %d", agents, len(m.visible))
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	bm, ok := cmd().(bulkWriteMsg)
	if !ok || bm.action != "flag" {
		t.Fatalf("bulk H should dispatch a bulk flag; got %+v", bm)
	}
	model, cmd := m.Update(bm)
	m = model.(Model)
	hm, ok := findHookMsg(cmd)
	if !ok {
		t.Fatal("bulk flag did not schedule the hook")
	}
	if len(f.calls) != agents || len(hm.lines) != agents {
		t.Errorf("hook calls = %v (want %d), lines = %v", f.calls, agents, hm.lines)
	}
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "handoff ") {
			t.Errorf("unexpected event in %q", c)
		}
	}
	model, _ = m.Update(hm)
	m = model.(Model)
	if !strings.Contains(m.status, "hook (handoff): ") {
		t.Errorf("status = %q, want the hook outcome", m.status)
	}
	if agents > 1 && !strings.Contains(m.status, "mirrored") {
		t.Errorf("status = %q, want a mirrored count for %d rows", m.status, agents)
	}
}

// Several successes collapse to a count; a mix reports both sides.
func TestHook_ResultBannerShapes(t *testing.T) {
	m, _ := hookedModel(t, &stubFirer{wants: true})
	model, cmd := m.Update(hookMsg{event: "handoff", lines: []string{"a-1: ok", "a-2: ok"}})
	if got := model.(Model).status; got != "hook (handoff): 2 mirrored" || cmd == nil {
		t.Errorf("status = %q (cmd nil=%v)", got, cmd == nil)
	}
	model, cmd = m.Update(hookMsg{event: "close", lines: []string{"a-1: ok"}, errs: []string{"a-3: boom"}})
	got := model.(Model).status
	if !strings.Contains(got, "1 ok, 1 failed") || !strings.Contains(got, "bd is written") || !strings.Contains(got, "a-3: boom") || cmd != nil {
		t.Errorf("status = %q (cmd nil=%v)", got, cmd == nil)
	}
}

// No hook, or a hook that does not want the event: nothing is spawned.
func TestHook_NotWantedSpawnsNothing(t *testing.T) {
	f := &stubFirer{wants: false}
	m, _ := hookedModel(t, f)
	human := m.visible[0]
	_, cmd := m.Update(writeMsg{action: "unflag", id: human.ID, issue: human})
	if _, ok := findHookMsg(cmd); ok || len(f.calls) != 0 {
		t.Errorf("hook spawned although not wanted; calls=%v", f.calls)
	}
	s := &stubMutator{stubSource: stubSource{issues: sampleIssues()}}
	bare := applyMutatorFetched(New(s), s)
	_, cmd = bare.Update(writeMsg{action: "flag", id: human.ID, issue: human})
	if _, ok := findHookMsg(cmd); ok {
		t.Error("a model without a hook scheduled one")
	}
}

// A failed hook is sticky and says bd is already written, so the user
// does not redo the TUI action and make a duplicate.
func TestHook_FailureIsStickyAndNamesTheState(t *testing.T) {
	f := &stubFirer{wants: true, err: errors.New("exited 1: no API key; replay: wyk hook dispatch handoff wyk-2")}
	m, _ := hookedModel(t, f)
	agent := m.visible[1]
	_, cmd := m.Update(writeMsg{action: "flag", id: agent.ID, issue: agent})
	hm, ok := findHookMsg(cmd)
	if !ok || len(hm.errs) != 1 {
		t.Fatalf("expected one failure, got ok=%v %+v", ok, hm)
	}
	model, cmd := m.Update(hm)
	m = model.(Model)
	for _, want := range []string{"hook (handoff) failed", "bd is written", agent.ID, "no API key", "wyk hook dispatch"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("status %q missing %q", m.status, want)
		}
	}
	if cmd != nil {
		t.Error("a hook failure must stay on screen (no flash-clear)")
	}
}

// The label prompt with "human" is the same transition as H.
func TestHook_LabelPromptHumanFiresHandoff(t *testing.T) {
	f := &stubFirer{wants: true}
	m, _ := hookedModel(t, f)
	agent := m.visible[1]
	_, cmd := m.Update(writeMsg{action: "label:human", id: agent.ID, issue: agent})
	if hm, ok := findHookMsg(cmd); !ok || hm.event != hookEventHandoff {
		t.Errorf("label:human should fire handoff; got ok=%v %+v", ok, hm)
	}
	_, cmd = m.Update(writeMsg{action: "label:urgent", id: agent.ID, issue: agent})
	if _, ok := findHookMsg(cmd); ok {
		t.Error("an unrelated label must not reach the hook")
	}
}
