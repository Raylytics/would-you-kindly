package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jimbottle/would-you-kindly/internal/beads"
	"github.com/jimbottle/would-you-kindly/internal/filter"
)

// Rework of PR #18's behaviors onto the preset-tagged fetchedMsg
// architecture (would-you-kindly-tjiy).

func TestHumanKey_TogglesBackToOriginPreset(t *testing.T) {
	src := &stubSource{issues: manyIssues(5)}
	m := applyFetched(New(src), src)

	// Start from blocked, jump into human, toggle back out.
	model, _ := m.switchPreset(filter.PresetBlocked)
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = model.(Model)
	if m.preset != filter.PresetHuman {
		t.Fatalf("h should enter the human view; got %v", m.preset)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = model.(Model)
	if m.preset != filter.PresetBlocked {
		t.Errorf("second h should return to the origin preset (blocked); got %v", m.preset)
	}
}

func TestHumanKey_ToggleFallsBackToAll(t *testing.T) {
	// Launching directly into human (wyk -preset human) has no
	// recorded origin: the toggle must land on all, not strand the
	// user.
	src := &stubSource{issues: manyIssues(3)}
	m := applyFetched(New(src).WithPreset(filter.PresetHuman), src)
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = model.(Model)
	if m.preset != filter.PresetAll {
		t.Errorf("h from human with no origin should land on all; got %v", m.preset)
	}
}

func TestSwitchPreset_PaintsCachedRowsInstantly(t *testing.T) {
	src := &stubSource{issues: manyIssues(4)}
	m := applyFetched(New(src), src) // seeds presetCache[all]

	// Land a human-preset fetch so its rows are cached too.
	humanRows := []beads.Issue{{ID: "h-1", Title: "human row"}}
	model, _ := m.switchPreset(filter.PresetHuman)
	m = model.(Model)
	model, _ = m.Update(fetchedMsg{preset: filter.PresetHuman, issues: humanRows})
	m = model.(Model)

	// Back to all: the cached all-rows must paint on THIS frame,
	// before any fetch returns.
	model, _ = m.switchPreset(filter.PresetAll)
	m = model.(Model)
	if len(m.visible) != 4 {
		t.Errorf("switch to all should instantly paint the 4 cached rows; visible=%d", len(m.visible))
	}
	// And back to human: its single cached row paints instantly.
	model, _ = m.switchPreset(filter.PresetHuman)
	m = model.(Model)
	if len(m.visible) != 1 || m.visible[0].ID != "h-1" {
		t.Errorf("switch to human should instantly paint its cached row; got %v", idsOf(m.visible))
	}
	if !m.refreshing {
		t.Error("instant paint must still dispatch the reconciling fetch (refreshing=true)")
	}
}

func TestToggleShowClosed_DropsPresetCache(t *testing.T) {
	// Cached rows were fetched under the old closed-scope; keeping
	// them would paint the wrong set after C.
	src := &stubSource{issues: manyIssues(3)}
	m := applyFetched(New(src), src)
	if len(m.presetCache) == 0 {
		t.Fatal("setup: fetch should seed the preset cache")
	}
	model, _ := m.toggleShowClosed()
	m = model.(Model)
	if m.presetCache != nil {
		t.Error("C must drop the preset cache (stale closed-scope)")
	}
}

func TestWithCacheSnapshot_SeedsAcrossPresets(t *testing.T) {
	// Quit on all, relaunch restoring human: the all-superset
	// snapshot must seed the human view via the in-memory filter
	// instead of cold-starting into ~9s of spinner (PR #18's
	// 3a0934a, reworked).
	snap := Cache{
		Preset:  string(filter.PresetAll),
		SavedAt: time.Now(),
		Issues: []beads.Issue{
			{ID: "a-1", Title: "agent row", Status: "open"},
			{ID: "h-1", Title: "human row", Status: "open", Labels: []string{"human"}},
			{ID: "b-1", Title: "blocked row", Status: "blocked"},
		},
	}
	src := &stubSource{}
	m := New(src).WithPreset(filter.PresetHuman).WithCacheSnapshot(snap, "")
	if len(m.all) != 1 || m.all[0].ID != "h-1" {
		t.Errorf("human view should seed with just the human-labeled row; got %v", idsOf(m.all))
	}

	// blocked works the same way…
	m = New(src).WithPreset(filter.PresetBlocked).WithCacheSnapshot(snap, "")
	if len(m.all) != 1 || m.all[0].ID != "b-1" {
		t.Errorf("blocked view should seed with the blocked row; got %v", idsOf(m.all))
	}

	// …ready cannot be approximated (bd computes dep-readiness):
	// no seed, cold start.
	m = New(src).WithPreset(filter.PresetReady).WithCacheSnapshot(snap, "")
	if len(m.all) != 0 {
		t.Errorf("ready view must not seed from a field-predicate filter; got %v", idsOf(m.all))
	}

	// …and a non-all snapshot can't reconstruct a different view.
	humanSnap := snap
	humanSnap.Preset = string(filter.PresetHuman)
	m = New(src).WithPreset(filter.PresetAll).WithCacheSnapshot(humanSnap, "")
	if len(m.all) != 0 {
		t.Errorf("a subset snapshot must not seed the all view; got %v", idsOf(m.all))
	}
}

func TestPresetCache_ImmuneToOptimisticMutation(t *testing.T) {
	// The cache must hold its own copies: optimisticListUpdate's close
	// path shifts m.all's backing array in place, and a cache entry
	// aliasing it would replay a corrupted slice (duplicated last row)
	// on the next instant paint (roborev #2062). fetch → optimistic
	// close → switch away and back → exactly the un-closed rows, no
	// duplicates.
	src := &stubSource{issues: manyIssues(4)}
	m := applyFetched(New(src), src) // caches the 4 all-rows

	victim := m.all[1]
	m.optimisticListUpdate("close", victim)
	if len(m.all) != 3 {
		t.Fatalf("setup: optimistic close should drop the row from m.all; len=%d", len(m.all))
	}

	// Away and back: the instant paint must be the pristine 4-row
	// fetch result, not the shifted array.
	model, _ := m.switchPreset(filter.PresetHuman)
	m = model.(Model)
	model, _ = m.switchPreset(filter.PresetAll)
	m = model.(Model)
	seen := map[string]int{}
	for _, i := range m.all {
		seen[i.ID]++
		if seen[i.ID] > 1 {
			t.Fatalf("duplicated row %q after cache paint — cache aliased the mutated array; rows: %v", i.ID, idsOf(m.all))
		}
	}
	if len(m.all) != 4 {
		t.Errorf("cache paint should restore the pristine 4-row fetch; got %v", idsOf(m.all))
	}
}

func TestWithCacheSnapshot_MinePresetAndClosedRows(t *testing.T) {
	// The mine predicate's two branches, both closed-filtered: the
	// snapshot may have been saved while showClosed was on, and
	// sessions relaunch closed-excluded (roborev #2061/#2062).
	snap := Cache{
		Preset:  string(filter.PresetAll),
		SavedAt: time.Now(),
		Issues: []beads.Issue{
			{ID: "m-1", Assignee: "ev", Status: "open"},
			{ID: "m-2", Assignee: "ev", Status: "closed"}, // closed: must not seed
			{ID: "o-1", Assignee: "someone-else", Status: "open"},
			{ID: "h-c", Status: "closed", Labels: []string{"human"}}, // closed human
		},
	}
	src := &stubSource{}

	// Identity set: only the open row assigned to me.
	m := New(src).WithMe("ev").WithPreset(filter.PresetMine).WithCacheSnapshot(snap, "")
	if len(m.all) != 1 || m.all[0].ID != "m-1" {
		t.Errorf("mine with identity should seed open assignee=ev rows only; got %v", idsOf(m.all))
	}

	// No identity: degrades to the non-closed set (the two open rows).
	m = New(src).WithPreset(filter.PresetMine).WithCacheSnapshot(snap, "")
	if len(m.all) != 2 {
		t.Errorf("mine without identity should seed the non-closed rows; got %v", idsOf(m.all))
	}

	// And the human predicate excludes the closed human row.
	m = New(src).WithPreset(filter.PresetHuman).WithCacheSnapshot(snap, "")
	if len(m.all) != 0 {
		t.Errorf("a closed human row must not seed the human view; got %v", idsOf(m.all))
	}
}

func TestWithCacheSnapshot_SamePresetStripsClosedRows(t *testing.T) {
	// Quit on all with C (show-closed) toggled on, relaunch on all:
	// the snapshot carries closed rows but the relaunch is always
	// closed-excluded (SessionState doesn't persist the toggle), so
	// the same-preset seed must strip them just like the cross-preset
	// predicates do (roborev #2063).
	snap := Cache{
		Preset:  string(filter.PresetAll),
		SavedAt: time.Now(),
		Issues: []beads.Issue{
			{ID: "open-1", Status: "open"},
			{ID: "closed-1", Status: "closed"},
		},
	}
	m := New(&stubSource{}).WithCacheSnapshot(snap, "")
	if len(m.all) != 1 || m.all[0].ID != "open-1" {
		t.Errorf("same-preset seed must exclude closed rows; got %v", idsOf(m.all))
	}
}

// TestSwitchPreset_DerivesFromAllSuperset: a switch to a preset that
// has never been fetched, but whose rows are a plain predicate over
// the cached "all" superset, paints the derived subset on the same
// frame — flagged approx in the status bar until the live fetch
// replaces it (would-you-kindly-ubhs).
func TestSwitchPreset_DerivesFromAllSuperset(t *testing.T) {
	rows := []beads.Issue{
		{ID: "a-1", Title: "plain", Status: "open", Labels: []string{}},
		{ID: "a-2", Title: "flagged", Status: "open", Labels: []string{"human"}},
		{ID: "a-3", Title: "flagged but closed", Status: "closed", Labels: []string{"human"}},
		{ID: "a-4", Title: "stuck", Status: "blocked", Labels: []string{}},
	}
	src := &stubSource{issues: rows}
	m := applyFetched(New(src), src) // seeds presetCache[all]

	model, _ := m.switchPreset(filter.PresetHuman)
	m = model.(Model)
	if got := idsOf(m.visible); len(got) != 1 || got[0] != "a-2" {
		t.Fatalf("human switch should paint the derived human subset instantly; got %v", got)
	}
	if !m.rowsApprox {
		t.Error("derived rows must be flagged approx")
	}
	if m.rowsStale() {
		t.Error("derived rows belong to the new preset — no stale band")
	}
	if !m.refreshing {
		t.Error("derived paint must still dispatch the reconciling fetch")
	}
	if bar := m.statusBar(); !strings.Contains(bar, "≈ approx") {
		t.Errorf("status bar should carry the approx marker; got %q", bar)
	}

	// The live fetch lands and clears the marker.
	live := []beads.Issue{{ID: "a-2", Title: "flagged", Status: "open", Labels: []string{"human"}}}
	model, _ = m.Update(fetchedMsg{preset: filter.PresetHuman, issues: live})
	m = model.(Model)
	if m.rowsApprox {
		t.Error("live fetch should clear the approx flag")
	}
	if bar := m.statusBar(); strings.Contains(bar, "approx") {
		t.Errorf("approx marker should be gone after the live fetch; got %q", bar)
	}

	// Blocked derives too; the switch goes through the cache for
	// human now, so this exercises a second predicate.
	model, _ = m.switchPreset(filter.PresetBlocked)
	m = model.(Model)
	if got := idsOf(m.visible); len(got) != 1 || got[0] != "a-4" {
		t.Errorf("blocked switch should paint the derived blocked subset; got %v", got)
	}
}

// TestSwitchPreset_ReadyShowsStaleBand: ready can't be derived, so
// the old rows stay up — and the view must say so with a band above
// the table (counted in chromeExtra so the row budget stays right),
// not just the status-line glyph.
func TestSwitchPreset_ReadyShowsStaleBand(t *testing.T) {
	src := &stubSource{issues: manyIssues(3)}
	m := applyFetched(New(src), src)
	m.width, m.height = 100, 30
	before := m.chromeExtra()

	model, _ := m.switchPreset(filter.PresetReady)
	m = model.(Model)
	if !m.rowsStale() {
		t.Fatal("ready switch with no cache should leave the rows stale")
	}
	if len(m.all) != 3 {
		t.Errorf("stale rows must stay on screen; got %d", len(m.all))
	}
	out := m.View()
	if !strings.Contains(out, "loading ready") || !strings.Contains(out, "[all] view") {
		t.Errorf("view should carry the stale-rows band naming both presets; got:\n%s", out)
	}
	// chromeExtra gains the band line; the chip strip also appears
	// (all → ready), so expect exactly two more lines.
	if got := m.chromeExtra(); got != before+2 {
		t.Errorf("chromeExtra should count band + chip strip; got %d, want %d", got, before+2)
	}

	model, _ = m.Update(fetchedMsg{preset: filter.PresetReady, issues: manyIssues(2)})
	m = model.(Model)
	if m.rowsStale() {
		t.Error("landing the ready fetch should clear the stale state")
	}
	if strings.Contains(m.View(), "loading ready") {
		t.Error("band should disappear once the ready rows land")
	}
}

// ctxSource is a Source whose Fetch honours cancellation the way the
// real bd client does: a cancelled context yields an error instead of
// rows.
type ctxSource struct{ issues []beads.Issue }

func (s *ctxSource) Fetch(ctx context.Context, _ filter.Preset) ([]beads.Issue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.issues, nil
}

// TestFetchCmd_CancelsPreviousFetch: dispatching a fetch cancels the
// one before it, and the cancelled command yields no message at all —
// so a superseded round-trip can neither clobber the rows nor clear
// refreshing under the fetch that replaced it.
func TestFetchCmd_CancelsPreviousFetch(t *testing.T) {
	src := &ctxSource{issues: manyIssues(2)}
	m := New(src)

	first := m.fetchCmd()
	_, second := m.switchPreset(filter.PresetHuman)

	if msg := first(); msg != nil {
		t.Errorf("superseded fetch should produce no message; got %#v", msg)
	}
	msg, ok := second().(fetchedMsg)
	if !ok {
		t.Fatalf("live fetch should produce a fetchedMsg; got %#v", msg)
	}
	if msg.err != nil || msg.preset != filter.PresetHuman || len(msg.issues) != 2 {
		t.Errorf("live fetch should land cleanly for the new preset; got %+v", msg)
	}

	// Bare Model{} (no controller) still fetches.
	bare := Model{src: src}
	if _, ok := bare.fetchCmd()().(fetchedMsg); !ok {
		t.Error("a model without a fetchController should still fetch")
	}
}

// TestSwitchPreset_DerivesFromDiskSidecar: a launch that restores onto
// a non-all preset has no live "all" fetch to derive from, so the
// sidecar carried in the on-disk cache stands in — flagged with its
// age — until the live all view is visited (would-you-kindly-rqo9).
func TestSwitchPreset_DerivesFromDiskSidecar(t *testing.T) {
	allAt := time.Now().Add(-3 * time.Hour)
	seed := Cache{
		Preset:  string(filter.PresetHuman),
		SavedAt: time.Now().Add(-time.Hour),
		Issues:  []beads.Issue{{ID: "h-1", Title: "flagged", Status: "open", Labels: []string{"human"}}},
		All: &AllSnapshot{SavedAt: allAt, Issues: []beads.Issue{
			{ID: "h-1", Title: "flagged", Status: "open", Labels: []string{"human"}},
			{ID: "b-1", Title: "stuck", Status: "blocked", Labels: []string{}},
			{ID: "c-1", Title: "gone", Status: "closed", Labels: []string{}},
		}},
	}
	src := &stubSource{}
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "last-fetch.json")
	m := New(src).WithPreset(filter.PresetHuman).WithCacheSnapshot(seed, cachePath)
	if got := idsOf(m.visible); len(got) != 1 || got[0] != "h-1" {
		t.Fatalf("restore onto human should seed the human rows; got %v", got)
	}
	if len(m.allSnap) != 2 {
		t.Fatalf("sidecar should be kept (closed stripped) as the all superset; got %d", len(m.allSnap))
	}

	// Tab to blocked: no presetCache at all yet, derive from disk.
	model, _ := m.switchPreset(filter.PresetBlocked)
	m = model.(Model)
	if got := idsOf(m.visible); len(got) != 1 || got[0] != "b-1" {
		t.Errorf("blocked should derive from the disk sidecar; got %v", got)
	}
	if !m.rowsApprox || m.rowsApproxAt.IsZero() {
		t.Error("disk-derived rows must be approx with an age")
	}
	if bar := m.statusBar(); !strings.Contains(bar, "≈ approx from cache 3h ago") || strings.Contains(bar, "  cached ") {
		t.Errorf("status should show the superset's age once, not also the warm-start 'cached' segment; got %q", bar)
	}

	// Switching to all paints the sidecar as-is, approx.
	model, _ = m.switchPreset(filter.PresetAll)
	m = model.(Model)
	if len(m.visible) != 2 || !m.rowsApprox {
		t.Errorf("all should paint the sidecar approx; got %v approx=%v", idsOf(m.visible), m.rowsApprox)
	}

	// A live blocked fetch lands: its save must carry the sidecar
	// forward so the NEXT launch has it too.
	model, _ = m.switchPreset(filter.PresetBlocked)
	m = model.(Model)
	model, cmd := m.Update(fetchedMsg{preset: filter.PresetBlocked, issues: []beads.Issue{{ID: "b-1", Status: "blocked"}}})
	m = model.(Model)
	if m.rowsApprox || !m.rowsApproxAt.IsZero() {
		t.Error("live fetch should clear approx and its age")
	}
	if cmd == nil {
		t.Fatal("expected the save cmd")
	}
	drainCmd(cmd)
	saved, err := LoadCache(cachePath)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	all, at := saved.AllSnapshot()
	if len(all) != 2 || !at.Equal(allAt) {
		t.Errorf("save from blocked should carry the disk sidecar forward; got %d rows at %v", len(all), at)
	}

	// A live all fetch replaces the sidecar; derives now carry no age.
	model, _ = m.switchPreset(filter.PresetAll)
	m = model.(Model)
	model, _ = m.Update(fetchedMsg{preset: filter.PresetAll, issues: []beads.Issue{{ID: "m-1", Status: "open", Assignee: "me", Labels: []string{}}}})
	m = model.(Model)
	m.me = "me"
	model, _ = m.switchPreset(filter.PresetMine)
	m = model.(Model)
	if got := idsOf(m.visible); len(got) != 1 || got[0] != "m-1" {
		t.Errorf("mine should derive from the live all fetch; got %v", got)
	}
	if !m.rowsApprox || !m.rowsApproxAt.IsZero() {
		t.Error("live-derived rows are approx without an age")
	}
}

// TestWithCacheSnapshot_SubsetSnapshotDerivesFromSidecar: quit on
// human, relaunch restoring mine — the main snapshot can't produce
// mine, but the sidecar can.
func TestWithCacheSnapshot_SubsetSnapshotDerivesFromSidecar(t *testing.T) {
	seed := Cache{
		Preset:  string(filter.PresetHuman),
		SavedAt: time.Now(),
		Issues:  []beads.Issue{{ID: "h-1", Status: "open", Labels: []string{"human"}}},
		All: &AllSnapshot{SavedAt: time.Now(), Issues: []beads.Issue{
			{ID: "h-1", Status: "open", Labels: []string{"human"}},
			{ID: "m-1", Status: "open", Assignee: "me", Labels: []string{}},
		}},
	}
	m := New(&stubSource{}).WithMe("me").WithPreset(filter.PresetMine).WithCacheSnapshot(seed, "")
	if got := idsOf(m.visible); len(got) != 1 || got[0] != "m-1" {
		t.Errorf("mine should be derived from the sidecar at warm-start; got %v", got)
	}
	if !m.cacheStale {
		t.Error("warm-start rows are cache-stale until the live fetch lands")
	}
}
