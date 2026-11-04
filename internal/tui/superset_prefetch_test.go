package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/jimbottle/would-you-kindly/internal/filter"
)

// The all-superset prefetch (would-you-kindly-t5ab): a session that
// launches on a subset preset (human, the restored default for many
// users) has no all superset for switchPreset to derive from, so the
// first switch to all — or to any preset — blocked on the full
// multi-repo fan-out (20s+ on a 30-workspace registry) under the
// "loading all… rows below are still the [human] view" band. The
// model now prefetches all in the background right after the first
// subset landing, so every later switch paints on the same frame.

func TestFirstSubsetFetchArmsSupersetPrefetch(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := New(src).WithPreset(filter.PresetHuman)
	if m.supersetFetching || m.supersetDone {
		t.Fatal("no prefetch before anything has landed")
	}

	model, cmd := m.Update(fetchedMsg{preset: filter.PresetHuman, issues: src.issues})
	m = model.(Model)
	if !m.supersetFetching || !m.supersetDone {
		t.Errorf("first human landing should arm the all prefetch; fetching=%v done=%v", m.supersetFetching, m.supersetDone)
	}
	if cmd == nil {
		t.Error("first human landing should return the prefetch cmd")
	}

	// The prefetch itself fetches all, flagged background, outside
	// the fetch controller (a plain stub source is enough to see it).
	msg, ok := m.supersetCmd()().(fetchedMsg)
	if !ok {
		t.Fatal("supersetCmd should yield a fetchedMsg")
	}
	if msg.preset != filter.PresetAll || !msg.background {
		t.Errorf("prefetch should be a background all fetch; got preset=%s background=%v", msg.preset, msg.background)
	}
	if src.last != filter.PresetAll {
		t.Errorf("prefetch should ask the source for all; got %s", src.last)
	}

	// Once per session: a later human landing does not re-arm it,
	// whether the prefetch is still in flight or already landed.
	model, _ = m.Update(fetchedMsg{preset: filter.PresetAll, issues: src.issues, background: true})
	m = model.(Model)
	if m.supersetFetching {
		t.Error("landing the prefetch should clear supersetFetching")
	}
	model, _ = m.Update(fetchedMsg{preset: filter.PresetHuman, issues: src.issues})
	m = model.(Model)
	if m.supersetFetching {
		t.Error("a second human landing must not re-arm the prefetch")
	}
}

func TestLaunchOnAllNeedsNoPrefetch(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src), src)
	if m.supersetFetching || m.supersetDone {
		t.Errorf("an all landing IS the superset; no prefetch; fetching=%v done=%v", m.supersetFetching, m.supersetDone)
	}
}

func TestBackgroundSupersetLandingStoresWithoutTouchingTheView(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src).WithPreset(filter.PresetHuman), src)
	m.width, m.height = 100, 30
	// Simulate a human tick in flight and a per-sub banner from it.
	m.refreshing = true
	m.fetchErrors = []FetchError{{Repo: "alpha", Err: errors.New("boom")}}
	rowsBefore := idsOfIssues(m.all)

	superset := manyIssues(5)
	model, _ := m.Update(fetchedMsg{preset: filter.PresetAll, issues: superset, background: true})
	m = model.(Model)

	if got := idsOfIssues(m.all); strings.Join(got, ",") != strings.Join(rowsBefore, ",") {
		t.Errorf("background landing must not touch the rows; got %v, want %v", got, rowsBefore)
	}
	if m.preset != filter.PresetHuman || m.rowsPreset != filter.PresetHuman {
		t.Errorf("background landing must not move the view; preset=%s rowsPreset=%s", m.preset, m.rowsPreset)
	}
	if !m.refreshing {
		t.Error("background landing must not clear the refreshing flag of the fetch the user is watching")
	}
	if len(m.fetchErrors) != 1 {
		t.Errorf("background landing must not replace the per-sub banner; got %v", m.fetchErrors)
	}
	if len(m.presetCache[filter.PresetAll]) != 5 || len(m.allSnap) != 5 {
		t.Errorf("background landing should file the superset; presetCache[all]=%d allSnap=%d", len(m.presetCache[filter.PresetAll]), len(m.allSnap))
	}

	// …and that is exactly what makes the next switch instant.
	m.refreshing = false
	model, _ = m.switchPreset(filter.PresetAll)
	m = model.(Model)
	if got := idsOfIssues(m.all); len(got) != 5 {
		t.Errorf("switch to all should paint the prefetched superset; got %v", got)
	}
	if m.rowsStale() || m.rowsApprox {
		t.Errorf("a prefetched all view is live, not stale/approx; stale=%v approx=%v", m.rowsStale(), m.rowsApprox)
	}
	if strings.Contains(m.View(), "loading all") {
		t.Error("no loading band when all painted from the prefetch")
	}
}

func TestBackgroundSupersetErrorIsDropped(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src).WithPreset(filter.PresetHuman), src)
	model, _ := m.Update(fetchedMsg{preset: filter.PresetAll, err: errors.New("bd exploded"), background: true})
	m = model.(Model)
	if m.supersetFetching {
		t.Error("a failed prefetch should clear supersetFetching")
	}
	if _, ok := m.presetCache[filter.PresetAll]; ok {
		t.Error("a failed prefetch must not file an empty superset")
	}
	if m.lastErr != nil {
		t.Errorf("a failed prefetch must not surface as the view's error; got %v", m.lastErr)
	}
}

func TestSwitchToAllReusesInFlightPrefetch(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src).WithPreset(filter.PresetHuman), src)
	m.width, m.height = 100, 30
	if !m.supersetFetching {
		t.Fatal("setup: prefetch should be in flight after the first human landing")
	}
	callsBefore := src.calls

	model, cmd := m.switchPreset(filter.PresetAll)
	m = model.(Model)
	if cmd != nil {
		t.Error("switch to all while the prefetch is in flight must not dispatch a duplicate fan-out")
	}
	if src.calls != callsBefore {
		t.Errorf("no extra source call expected; calls went %d → %d", callsBefore, src.calls)
	}
	if !m.refreshing || !m.rowsStale() {
		t.Errorf("until the prefetch lands the view is refreshing with the stale band; refreshing=%v stale=%v", m.refreshing, m.rowsStale())
	}
	if !strings.Contains(m.View(), "loading all") {
		t.Error("the loading band should name the in-flight all fetch")
	}

	// The prefetch lands as THIS view's fetch.
	superset := manyIssues(4)
	model, _ = m.Update(fetchedMsg{preset: filter.PresetAll, issues: superset, background: true})
	m = model.(Model)
	if m.refreshing || m.supersetFetching {
		t.Errorf("landing should clear refreshing and supersetFetching; got %v / %v", m.refreshing, m.supersetFetching)
	}
	if got := idsOfIssues(m.all); len(got) != 4 || m.rowsPreset != filter.PresetAll {
		t.Errorf("landing should paint the all rows; got %v (rowsPreset=%s)", got, m.rowsPreset)
	}
	if strings.Contains(m.View(), "loading all") {
		t.Error("band should drop once the prefetch lands")
	}
}

// A switch to a subset preset while the prefetch is in flight still
// dispatches its own fetch (only all can reuse the prefetch).
func TestSwitchToSubsetWhilePrefetchInFlightStillFetches(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src).WithPreset(filter.PresetHuman), src)
	model, cmd := m.switchPreset(filter.PresetBlocked)
	m = model.(Model)
	if cmd == nil {
		t.Error("switch to blocked should dispatch its own fetch")
	}
	if !m.supersetFetching {
		t.Error("the prefetch stays in flight across a subset switch")
	}
}
