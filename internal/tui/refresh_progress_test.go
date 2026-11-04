package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jimbottle/would-you-kindly/internal/beads"
	"github.com/jimbottle/would-you-kindly/internal/filter"
	"github.com/jimbottle/would-you-kindly/internal/workspace"
)

// fetchedFrom runs cmd — unwrapping any tea.Batch — and returns the
// first fetchedMsg it produces. Dispatch sites batch the fetch with a
// spinner tick (would-you-kindly-ocgy), so tests that used to call
// cmd() and type-assert go through this instead. Each command runs
// under a short deadline so a long timer in the batch (tickCmd's poll
// interval) can't stall the test.
func fetchedFrom(cmd tea.Cmd) (fetchedMsg, bool) {
	if cmd == nil {
		return fetchedMsg{}, false
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		switch v := msg.(type) {
		case fetchedMsg:
			return v, true
		case tea.BatchMsg:
			for _, c := range v {
				if fm, ok := fetchedFrom(c); ok {
					return fm, true
				}
			}
		}
	case <-time.After(time.Second):
	}
	return fetchedMsg{}, false
}

func TestReselectingInFlightPresetDoesNotRedispatch(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := New(src).WithPreset(filter.PresetHuman)
	m.all = sampleIssues()
	m.rowsPreset = filter.PresetAll // the band is up: rows are still all's
	m.beginRefresh()
	before := src.calls

	model, cmd := m.switchPreset(filter.PresetHuman)
	m = model.(Model)
	if _, fetched := fetchedFrom(cmd); fetched || src.calls != before {
		t.Fatalf("re-selecting the in-flight preset must not cancel and redispatch its fetch (fetched=%v calls %d→%d)", fetched, before, src.calls)
	}
	if !m.refreshing || m.preset != filter.PresetHuman {
		t.Errorf("state should be untouched; refreshing=%v preset=%s", m.refreshing, m.preset)
	}
}

func TestReselectingIdlePresetStillRefreshes(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := New(src).WithPreset(filter.PresetHuman)
	before := src.calls
	_, cmd := m.switchPreset(filter.PresetHuman)
	if _, fetched := fetchedFrom(cmd); !fetched || src.calls != before+1 {
		t.Fatalf("re-selecting with nothing in flight should refresh as before (fetched=%v calls %d→%d)", fetched, before, src.calls)
	}
}

func TestSwitchingAwayStillRedispatches(t *testing.T) {
	// The no-op is strictly for the SAME preset: a real switch during a
	// refresh keeps the cancel-and-redispatch behaviour
	// (would-you-kindly-ubhs).
	src := &stubSource{issues: sampleIssues()}
	m := New(src).WithPreset(filter.PresetHuman)
	m.beginRefresh()
	before := src.calls
	_, cmd := m.switchPreset(filter.PresetReady)
	if _, fetched := fetchedFrom(cmd); !fetched || src.calls != before+1 {
		t.Fatalf("a real switch must still fetch (fetched=%v calls %d→%d)", fetched, before, src.calls)
	}
}

// progressSource is a Source that also reports fan-out progress.
type progressSource struct {
	stubSource
	done, total int
}

func (p *progressSource) FetchProgress(filter.Preset) (int, int) { return p.done, p.total }

func TestRefreshProgressReadout(t *testing.T) {
	src := &progressSource{done: 12, total: 29}
	m := New(src)
	if got := m.refreshProgress(); got != "" {
		t.Errorf("idle model should have no readout; got %q", got)
	}
	m.refreshing = true
	m.refreshStarted = time.Now().Add(-18 * time.Second)
	if got := m.refreshProgress(); got != "12/29 repos · 18s" {
		t.Errorf("readout = %q, want \"12/29 repos · 18s\"", got)
	}
	// Under a second in: the tally shows, the time does not flash "0s".
	m.refreshStarted = time.Now()
	if got := m.refreshProgress(); got != "12/29 repos" {
		t.Errorf("fresh readout = %q, want \"12/29 repos\"", got)
	}
	// A source without progress still gets the elapsed time.
	plain := New(&stubSource{})
	plain.refreshing = true
	plain.refreshStarted = time.Now().Add(-3 * time.Second)
	if got := plain.refreshProgress(); got != "3s" {
		t.Errorf("single-source readout = %q, want \"3s\"", got)
	}
}

func TestStaleRowsBandCarriesProgress(t *testing.T) {
	band := renderStaleRowsBand(filter.PresetReady, filter.PresetHuman, "12/29 repos · 18s", 0)
	for _, want := range []string{"loading ready… 12/29 repos · 18s", "still the [human] view"} {
		if !strings.Contains(band, want) {
			t.Errorf("band %q lacks %q", band, want)
		}
	}
	bare := renderStaleRowsBand(filter.PresetReady, filter.PresetHuman, "", 0)
	if !strings.Contains(bare, "loading ready… —") {
		t.Errorf("band without progress should read as before; got %q", bare)
	}
}

func TestStatusBarShowsRefreshProgress(t *testing.T) {
	src := &progressSource{done: 3, total: 7}
	m := New(src)
	m.width, m.height = 120, 30
	m.all = sampleIssues()
	m.refreshing = true
	m.refreshStarted = time.Now().Add(-5 * time.Second)
	if view := m.View(); !strings.Contains(view, "↻ refreshing 3/7 repos · 5s") {
		t.Errorf("status bar should carry the readout; view:\n%s", view)
	}
}

func TestFetchLandingClearsRefreshStart(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := New(src)
	m.beginRefresh()
	model, _ := m.Update(fetchedMsg{preset: m.preset, issues: sampleIssues()})
	m = model.(Model)
	if m.refreshing || !m.refreshStarted.IsZero() {
		t.Errorf("landing should clear the in-flight marker; refreshing=%v started=%v", m.refreshing, m.refreshStarted)
	}
}

func TestSpinnerKeepsTickingWhileRefreshing(t *testing.T) {
	m := New(&stubSource{issues: sampleIssues()})
	m.loading = false
	m.refreshing = true
	if _, cmd := m.Update(m.spinner.Tick()); cmd == nil {
		t.Error("spinner should re-tick while a refresh is in flight so the elapsed readout updates")
	}
	m.refreshing = false
	if _, cmd := m.Update(m.spinner.Tick()); cmd != nil {
		t.Error("spinner should retire when nothing is in flight")
	}
}

func TestMultiBDSourceReportsFanOutProgress(t *testing.T) {
	a := &fakeRepoSource{issues: []beads.Issue{{ID: "alpha-1"}}}
	b := &fakeRepoSource{issues: []beads.Issue{{ID: "beta-1"}}}
	m := &MultiBDSource{subs: []subRepo{
		{name: "alpha", src: a, branchFn: func(context.Context) string { return "" }},
		{name: "beta", src: b, branchFn: func(context.Context) string { return "" }},
	}}
	if done, total := m.FetchProgress(filter.PresetAll); done != 0 || total != 0 {
		t.Fatalf("before any fetch: %d/%d, want 0/0", done, total)
	}
	if _, _, err := m.FetchWithSubErrors(context.Background(), filter.PresetAll); err != nil {
		t.Fatal(err)
	}
	if done, total := m.FetchProgress(filter.PresetAll); done != 2 || total != 2 {
		t.Errorf("after the fan-out: %d/%d, want 2/2", done, total)
	}
	if done, total := m.FetchProgress(filter.PresetReady); done != 0 || total != 0 {
		t.Errorf("other presets untouched: %d/%d, want 0/0", done, total)
	}
}

func TestMismatchedWorkspaceIsReportedNotFetched(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads", "embeddeddolt", "real_db"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRepoSource{issues: []beads.Issue{{ID: "alpha-1"}}}
	m := &MultiBDSource{subs: []subRepo{{
		name:     "alpha",
		src:      fake,
		path:     dir,
		branchFn: func(context.Context) string { return "" },
	}}}
	_, errs, err := m.FetchWithSubErrors(context.Background(), filter.PresetAll)
	if err == nil || len(errs) != 1 || errs[0].Repo != "alpha" {
		t.Fatalf("a workspace bd would read empty must surface as that repo's error; errs=%v err=%v", errs, err)
	}
	if !strings.Contains(errs[0].Err.Error(), "embeddeddolt/real_db") {
		t.Errorf("error should name the database the data is in; got %q", errs[0].Err)
	}
	if atomic.LoadInt32(&fake.fetchCount) != 0 {
		t.Errorf("bd must not be asked for a workspace it would misread; fetchCount=%d", fake.fetchCount)
	}

	// Restoring metadata.json heals it on the very next refresh.
	if err := os.WriteFile(filepath.Join(dir, ".beads", "metadata.json"), []byte(workspace.MinimalMetadata("real_db")), 0o600); err != nil {
		t.Fatal(err)
	}
	issues, errs, err := m.FetchWithSubErrors(context.Background(), filter.PresetAll)
	if err != nil || len(errs) != 0 || len(issues) != 1 {
		t.Fatalf("after restore: issues=%d errs=%v err=%v", len(issues), errs, err)
	}
}
