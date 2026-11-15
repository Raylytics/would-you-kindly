package tui

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jimbottle/would-you-kindly/internal/beads"
	"github.com/jimbottle/would-you-kindly/internal/filter"
)

// TestMultiBDSource_FetchCache exercises the per-repo fetch cache
// (would-you-kindly-jipr): unchanged repos are served from cache (no bd
// call) and invalidated by mtime change, preset change, InvalidateCache,
// SetIncludeClosed, and the TTL.
func TestMultiBDSource_FetchCache(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.Mkdir(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRepoSource{issues: []beads.Issue{{ID: "alpha-1", Title: "t"}}}
	m := &MultiBDSource{subs: []subRepo{{
		name:     "alpha",
		src:      fake,
		path:     dir,
		branchFn: func(_ context.Context) string { return "main" },
	}}}
	ctx := context.Background()
	count := func() int32 { return atomic.LoadInt32(&fake.fetchCount) }
	fetch := func(p filter.Preset) { _, _, _ = m.FetchWithSubErrors(ctx, p) }

	fetch(filter.PresetAll)
	if count() != 1 {
		t.Fatalf("first fetch count=%d, want 1", count())
	}
	fetch(filter.PresetAll) // unchanged → cache hit
	if count() != 1 {
		t.Errorf("unchanged repo should hit cache; count=%d, want 1", count())
	}

	// mtime bump → invalidate.
	future := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(beadsDir, future, future); err != nil {
		t.Fatal(err)
	}
	fetch(filter.PresetAll)
	if count() != 2 {
		t.Errorf("mtime change should re-fetch; count=%d, want 2", count())
	}
	fetch(filter.PresetAll) // same mtime → hit again
	if count() != 2 {
		t.Errorf("second fetch at same mtime should hit; count=%d, want 2", count())
	}

	// Manual `r` → InvalidateCache.
	m.InvalidateCache()
	fetch(filter.PresetAll)
	if count() != 3 {
		t.Errorf("InvalidateCache should force re-fetch; count=%d, want 3", count())
	}

	// Different preset is a different cache key…
	fetch(filter.PresetReady)
	if count() != 4 {
		t.Errorf("preset change should re-fetch; count=%d, want 4", count())
	}
	// …that COEXISTS with the first: the entries are keyed by
	// (repo, preset), so the background all prefetch and the current
	// view's ticks don't evict each other (would-you-kindly-t5ab).
	fetch(filter.PresetAll)
	fetch(filter.PresetReady)
	if count() != 4 {
		t.Errorf("per-preset entries should coexist; count=%d, want 4", count())
	}

	// SetIncludeClosed invalidates the whole cache.
	m.SetIncludeClosed(true)
	fetch(filter.PresetReady)
	if count() != 5 {
		t.Errorf("SetIncludeClosed should invalidate; count=%d, want 5", count())
	}

	// TTL backstop: backdate the entry → next fetch misses.
	m.cacheMu.Lock()
	e := m.cache[subCacheKey{"alpha", filter.PresetReady}]
	e.at = time.Now().Add(-2 * fetchCacheTTL)
	m.cache[subCacheKey{"alpha", filter.PresetReady}] = e
	m.cacheMu.Unlock()
	fetch(filter.PresetReady)
	if count() != 6 {
		t.Errorf("TTL expiry should re-fetch; count=%d, want 6", count())
	}
}

// TestMultiBDSource_NoCacheWithoutPath confirms a sub with no path (the
// stub/test shape) is never cached — every fetch runs live.
func TestMultiBDSource_NoCacheWithoutPath(t *testing.T) {
	fake := &fakeRepoSource{issues: []beads.Issue{{ID: "alpha-1"}}}
	m := &MultiBDSource{subs: []subRepo{{name: "alpha", src: fake, branchFn: func(_ context.Context) string { return "" }}}}
	for i := 0; i < 3; i++ {
		_, _, _ = m.FetchWithSubErrors(context.Background(), filter.PresetAll)
	}
	if got := atomic.LoadInt32(&fake.fetchCount); got != 3 {
		t.Errorf("no-path sub should fetch live every time; count=%d, want 3", got)
	}
}

// TestMultiBDSource_BranchReDerivedOnCacheHit pins roborev #1850/#1849:
// a cache hit must reflect the CURRENT git branch (re-derived live),
// not a branch frozen at cache time — a `git checkout` doesn't touch
// .beads so the cache stays valid, but the Branch must still update.
func TestMultiBDSource_BranchReDerivedOnCacheHit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRepoSource{issues: []beads.Issue{{ID: "alpha-1"}}}
	branch := "main"
	m := &MultiBDSource{subs: []subRepo{{
		name:     "alpha",
		src:      fake,
		path:     dir,
		branchFn: func(_ context.Context) string { return branch },
	}}}
	ctx := context.Background()

	all, _, _ := m.FetchWithSubErrors(ctx, filter.PresetAll) // miss → caches issues
	if len(all) != 1 || all[0].Branch != "main" {
		t.Fatalf("first fetch: branch=%q want main", all[0].Branch)
	}
	branch = "feature-x" // simulate a git checkout (does NOT touch .beads)
	all, _, _ = m.FetchWithSubErrors(ctx, filter.PresetAll)
	if got := atomic.LoadInt32(&fake.fetchCount); got != 1 {
		t.Errorf("second fetch should still be a cache hit (no bd call); count=%d want 1", got)
	}
	if len(all) != 1 || all[0].Branch != "feature-x" {
		t.Errorf("cache hit must re-derive the live branch; got %q want feature-x", all[0].Branch)
	}
}

func TestBeadsMtime_SeesEmbeddedDoltWrites(t *testing.T) {
	// bd's embedded Dolt backend commits under
	// .beads/embeddeddolt/<db>/.dolt/noms/, which does not change the
	// mtime of .beads itself. A close made there must still count as a
	// change, or the next fetch is served from the pre-close cache and
	// the closed row comes back (would-you-kindly-06rm).
	root := t.TempDir()
	noms := filepath.Join(root, ".beads", "embeddeddolt", "demo", ".dolt", "noms")
	if err := os.MkdirAll(noms, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(noms, "manifest")
	if err := os.WriteFile(manifest, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, p := range []string{manifest, noms, filepath.Join(root, ".beads")} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	before, ok := beadsMtime(root)
	if !ok {
		t.Fatal("beadsMtime should stat a .beads dir")
	}

	// A Dolt commit: only the manifest changes.
	later := old.Add(10 * time.Minute)
	if err := os.Chtimes(manifest, later, later); err != nil {
		t.Fatal(err)
	}
	after, _ := beadsMtime(root)
	if !after.After(before) {
		t.Errorf("a write under .beads/embeddeddolt must change the freshness stamp; before=%v after=%v", before, after)
	}
}

// invalidatingMutator is a stubMutator that records cache drops.
type invalidatingMutator struct {
	stubMutator
	drops int
}

func (s *invalidatingMutator) InvalidateCache() { s.drops++ }

func TestWriteResult_DropsFetchCache(t *testing.T) {
	// The refetch after a close must read bd, not the per-repo cache:
	// the cache cannot have seen the write yet (would-you-kindly-06rm).
	s := &invalidatingMutator{stubMutator: stubMutator{stubSource: stubSource{issues: sampleIssues()}}}
	m := New(s)
	model, _ := m.Update(fetchedMsg{preset: m.preset, issues: s.issues})
	m = model.(Model)
	m.Update(writeMsg{action: "close", id: "a-1", issue: m.visible[0]})
	if s.drops != 1 {
		t.Errorf("a landed close should drop the fetch cache once; got %d drops", s.drops)
	}
	m.Update(bulkWriteMsg{action: "close", total: 2})
	if s.drops != 2 {
		t.Errorf("a bulk write should drop the fetch cache too; got %d drops", s.drops)
	}
}
