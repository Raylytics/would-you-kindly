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
	if m.supersetInFlight() || m.supersetDone {
		t.Fatal("no prefetch before anything has landed")
	}

	model, cmd := m.Update(fetchedMsg{preset: filter.PresetHuman, issues: src.issues})
	m = model.(Model)
	if !m.supersetInFlight() || !m.supersetDone {
		t.Errorf("first human landing should arm the all prefetch; fetching=%v done=%v", m.supersetInFlight(), m.supersetDone)
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
	if m.supersetInFlight() {
		t.Error("landing the prefetch should clear supersetFetching")
	}
	model, _ = m.Update(fetchedMsg{preset: filter.PresetHuman, issues: src.issues})
	m = model.(Model)
	if m.supersetInFlight() {
		t.Error("a second human landing must not re-arm the prefetch")
	}
}

func TestLaunchOnAllNeedsNoPrefetch(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src), src)
	if m.supersetInFlight() || m.supersetDone {
		t.Errorf("an all landing IS the superset; no prefetch; fetching=%v done=%v", m.supersetInFlight(), m.supersetDone)
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
	if m.supersetInFlight() {
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
	if !m.supersetInFlight() {
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
	if m.refreshing || m.supersetInFlight() {
		t.Errorf("landing should clear refreshing and supersetFetching; got %v / %v", m.refreshing, m.supersetInFlight())
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
	if !m.supersetInFlight() {
		t.Error("the prefetch stays in flight across a subset switch")
	}
}

// roborev #6902: the prefetch runs under the controller's own
// context so a result that can no longer be trusted never lands.

func TestToggleShowClosedCancelsAndRearmsPrefetch(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src).WithPreset(filter.PresetHuman), src)
	if !m.supersetInFlight() {
		t.Fatal("setup: prefetch in flight after the first human landing")
	}

	model, _ := m.toggleShowClosed()
	m = model.(Model)
	if m.supersetInFlight() {
		t.Error("C must cancel the in-flight prefetch: its rows are the old closed-scope")
	}
	if m.supersetDone {
		t.Error("C must re-arm the prefetch so the new scope gets its own superset")
	}

	// The old-scope result arrives anyway (a fabricated late landing;
	// in production the cancelled closure yields nil): dropped.
	model, _ = m.Update(fetchedMsg{preset: filter.PresetAll, issues: manyIssues(5), background: true})
	m = model.(Model)
	if _, ok := m.presetCache[filter.PresetAll]; ok {
		t.Error("a cancelled prefetch must not refill presetCache[all]")
	}
	if len(m.allSnap) != 0 {
		t.Error("a cancelled prefetch must not become the sidecar")
	}
	model, cmd := m.switchPreset(filter.PresetAll)
	m = model.(Model)
	if cmd == nil || !m.rowsStale() {
		t.Error("with no trusted superset, the switch to all must dispatch its own fetch")
	}

	// The next landing under the new scope arms a fresh prefetch.
	m = applyFetched(New(src).WithPreset(filter.PresetHuman), src)
	model, _ = m.toggleShowClosed()
	m = model.(Model)
	model, _ = m.Update(fetchedMsg{preset: filter.PresetHuman, issues: src.issues})
	m = model.(Model)
	if !m.supersetInFlight() || !m.supersetDone {
		t.Errorf("the first landing after C should arm a fresh prefetch; inFlight=%v done=%v", m.supersetInFlight(), m.supersetDone)
	}
}

func TestAdoptedPrefetchIsSupersededByNewerFetchOnAll(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src).WithPreset(filter.PresetHuman), src)
	model, _ := m.switchPreset(filter.PresetAll)
	m = model.(Model)
	if !m.supersetInFlight() {
		t.Fatal("setup: the switch to all should have adopted the prefetch")
	}

	// A manual r on all dispatches a fresh fetch: the adopted
	// prefetch is now the older answer and must be superseded.
	model, cmd := m.manualRefresh()
	m = model.(Model)
	if cmd == nil {
		t.Fatal("manual refresh should dispatch a fetch")
	}
	if m.supersetInFlight() {
		t.Error("a newer fetch for the all view must supersede the adopted prefetch")
	}
	rowsBefore := len(m.all)
	model, _ = m.Update(fetchedMsg{preset: filter.PresetAll, issues: manyIssues(7), background: true})
	m = model.(Model)
	if len(m.all) != rowsBefore || !m.refreshing {
		t.Errorf("the superseded prefetch must not land over the newer fetch; rows %d→%d refreshing=%v", rowsBefore, len(m.all), m.refreshing)
	}
	model, _ = m.Update(fetchedMsg{preset: filter.PresetAll, issues: manyIssues(2)})
	m = model.(Model)
	if len(m.all) != 2 || m.refreshing {
		t.Errorf("the newer fetch lands normally; rows=%d refreshing=%v", len(m.all), m.refreshing)
	}
}

func TestFetchControllerSupersetContexts(t *testing.T) {
	f := &fetchController{}
	// Not adopted: a primary begin leaves the prefetch alone.
	sctx := f.beginSuperset()
	_ = f.begin()
	if sctx.Err() != nil {
		t.Fatal("an unadopted prefetch must survive a primary dispatch")
	}
	// Adopted: the next begin cancels it and the slot empties.
	pctx := f.begin()
	if !f.adoptSuperset() {
		t.Fatal("adopt should succeed while the prefetch is in flight")
	}
	if pctx.Err() == nil {
		t.Error("adopting should cancel the primary fetch it replaces")
	}
	_ = f.begin()
	if sctx.Err() == nil || f.supersetInFlight() {
		t.Error("a begin after adoption must cancel the prefetch")
	}
	if f.adoptSuperset() {
		t.Error("nothing left to adopt")
	}
	if f.supersetLanded() {
		t.Error("a landing after cancellation is stale")
	}
	// cancelSuperset ends it without touching the primary.
	sctx = f.beginSuperset()
	pctx = f.begin()
	f.cancelSuperset()
	if sctx.Err() == nil || pctx.Err() != nil {
		t.Error("cancelSuperset should cancel only the prefetch")
	}
	// A normal landing empties the slot exactly once.
	_ = f.beginSuperset()
	if !f.supersetLanded() || f.supersetLanded() {
		t.Error("supersetLanded should report true once, then false")
	}
}

// roborev #6906: leaving all before the adopted prefetch lands must
// not cancel it — nothing the next preset fetches can land over all
// rows, and cancelling would strand the session without a superset.
func TestLeavingAllReturnsAdoptedPrefetchToBackground(t *testing.T) {
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src).WithPreset(filter.PresetHuman), src)
	model, _ := m.switchPreset(filter.PresetAll)
	m = model.(Model)
	if !m.supersetInFlight() {
		t.Fatal("setup: the switch to all should have adopted the prefetch")
	}

	model, cmd := m.switchPreset(filter.PresetBlocked)
	m = model.(Model)
	if cmd == nil {
		t.Error("switch to blocked should dispatch its own fetch")
	}
	if !m.supersetInFlight() {
		t.Fatal("leaving all must keep the prefetch running")
	}

	// It lands as a background result and fills the superset…
	superset := manyIssues(6)
	model, _ = m.Update(fetchedMsg{preset: filter.PresetAll, issues: superset, background: true})
	m = model.(Model)
	if m.preset != filter.PresetBlocked || !m.refreshing {
		t.Errorf("background landing must not touch the blocked view; preset=%s refreshing=%v", m.preset, m.refreshing)
	}
	if len(m.presetCache[filter.PresetAll]) != 6 {
		t.Errorf("prefetch should fill presetCache[all]; got %d", len(m.presetCache[filter.PresetAll]))
	}

	// …so the next switch back to all is instant.
	model, _ = m.switchPreset(filter.PresetAll)
	m = model.(Model)
	if len(m.all) != 6 || m.rowsStale() {
		t.Errorf("switch back to all should paint the prefetched superset; rows=%d stale=%v", len(m.all), m.rowsStale())
	}
}

func TestFetchControllerUnadopt(t *testing.T) {
	f := &fetchController{}
	sctx := f.beginSuperset()
	if !f.adoptSuperset() {
		t.Fatal("adopt should succeed")
	}
	f.unadoptSuperset()
	_ = f.begin()
	if sctx.Err() != nil || !f.supersetInFlight() {
		t.Error("after unadopt a primary begin must leave the prefetch alone")
	}
	// Re-adopting puts it back under supersession.
	if !f.adoptSuperset() {
		t.Fatal("re-adopt should succeed")
	}
	_ = f.begin()
	if sctx.Err() == nil || f.supersetInFlight() {
		t.Error("a begin after re-adoption must cancel the prefetch")
	}
}
