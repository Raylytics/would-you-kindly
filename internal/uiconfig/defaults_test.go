package uiconfig

import "testing"

func TestHiddenSet_SessionHiddenUnlessShown(t *testing.T) {
	if !(Config{}).HiddenSet()["session"] {
		t.Error("Session should start hidden")
	}
	c := Config{ShownColumns: []string{"session"}, HiddenColumns: []string{"type"}}
	h := c.HiddenSet()
	if h["session"] || !h["type"] {
		t.Errorf("shown_columns should switch a default-hidden column on; got %v", h)
	}
}
