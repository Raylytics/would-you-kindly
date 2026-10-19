package beads

import "testing"

// TestIsAgentOwned pins the owner-badge predicate: an issue is agent-owned
// (AGENT / HUMAN-BLOCK in the TUI) when it is NOT `human`-flagged and carries
// a src label. A human FILING a task (`src:human`) is agent-owned work unless
// the task is also flagged `human`. responsibilityBadgeFor and the doctor's
// owner guard both lean on this.
func TestIsAgentOwned(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		want   bool
	}{
		{"src:agent", []string{"src:agent"}, true},
		{"src:human (human-filed agent work)", []string{"src:human"}, true},
		{"human only", []string{"human"}, false},
		{"human + src:agent (bounced back)", []string{"human", "src:agent"}, false},
		{"human + src:human", []string{"human", "src:human"}, false},
		{"no labels", nil, false},
		{"unrelated label only", []string{"priority:hi"}, false},
	}
	for _, c := range cases {
		if got := (Issue{Labels: c.labels}).IsAgentOwned(); got != c.want {
			t.Errorf("%s: IsAgentOwned(%v) = %v, want %v", c.name, c.labels, got, c.want)
		}
	}
}

func TestBlockingDependencies(t *testing.T) {
	i := Issue{Dependencies: []Dependency{
		{DependsOnID: "a", Type: "blocks"},
		{DependsOnID: "b", Type: "parent-child"},
		{DependsOnID: "c", Type: ""},
		{DependsOnID: "", Type: "blocks"},
		{DependsOnID: "d", Type: "related"},
	}}
	got := i.BlockingDependencies()
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("BlockingDependencies = %v, want [a c]", got)
	}
	if got := (Issue{}).BlockingDependencies(); got != nil {
		t.Errorf("no edges should yield nil, got %v", got)
	}
}
