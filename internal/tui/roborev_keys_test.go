package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// These tests pin the keys wyk shares with roborev's TUI (see the
// keyMap doc comment). A rebind that breaks one of them breaks the
// "same key, same job" promise between the two tools.

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestRoborevKeys_CommentKeyOpensNote(t *testing.T) {
	// `c` is roborev's comment key; `n` is the older wyk alias.
	for _, r := range []rune{'c', 'n'} {
		s := &stubMutator{stubSource: stubSource{issues: sampleIssues()}}
		m := applyMutatorFetched(New(s), s)
		model, _ := m.Update(runeKey(r))
		if got := model.(Model).mode; got != modeNote {
			t.Errorf("%q in the list should enter modeNote; got %v", r, got)
		}

		s = &stubMutator{stubSource: stubSource{issues: sampleIssues()}}
		m = enterDetailWithMutator(t, s)
		model, _ = m.Update(runeKey(r))
		d := model.(Model)
		if d.mode != modeNote || d.promptReturn != modeDetail {
			t.Errorf("%q in detail should enter modeNote returning to detail; got mode=%v ret=%v", r, d.mode, d.promptReturn)
		}
	}
}

func TestRoborevKeys_CInDetailNoLongerCopies(t *testing.T) {
	s := &stubMutator{stubSource: stubSource{issues: sampleIssues()}}
	m := enterDetailWithMutator(t, s)
	copied := false
	orig := clipboardCopy
	clipboardCopy = func(string) error { copied = true; return nil }
	t.Cleanup(func() { clipboardCopy = orig })

	m.Update(runeKey('c'))
	if copied {
		t.Error("c in detail must not copy any more — y is the copy key")
	}
}

func TestRoborevKeys_FilterKey(t *testing.T) {
	for _, r := range []rune{'f', '/'} {
		src := &stubSource{issues: sampleIssues()}
		m := applyFetched(New(src), src)
		model, _ := m.Update(runeKey(r))
		if got := model.(Model).mode; got != modeFilter {
			t.Errorf("%q should open the filter prompt; got %v", r, got)
		}
	}

	// Inside the prompt `f` is text, even as the first character: only
	// `/` closes an empty prompt.
	src := &stubSource{issues: sampleIssues()}
	m := applyFetched(New(src), src)
	model, _ := m.Update(runeKey('f'))
	model, _ = model.(Model).Update(runeKey('f'))
	m = model.(Model)
	if m.mode != modeFilter || m.input.Value() != "f" {
		t.Fatalf("f inside the prompt should type; got mode=%v value=%q", m.mode, m.input.Value())
	}
}

func TestRoborevKeys_ListPagingAndAliases(t *testing.T) {
	m, _ := splitFixture(t, 100, 20) // 30 rows, more than one screen
	page := m.bodyHeight()
	if page < 2 || page >= len(m.visible) {
		t.Fatalf("precondition: want a multi-page list; body=%d rows=%d", page, len(m.visible))
	}
	press := func(k tea.KeyMsg) {
		t.Helper()
		model, _ := m.Update(k)
		m = model.(Model)
	}

	press(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.cursor != page {
		t.Errorf("pgdn should move one page (%d); cursor=%d", page, m.cursor)
	}
	if m.cursor < m.scroll || m.cursor >= m.scroll+page {
		t.Errorf("cursor %d should stay inside the window starting at %d", m.cursor, m.scroll)
	}
	press(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.cursor != 0 {
		t.Errorf("pgup should return to the top; cursor=%d", m.cursor)
	}
	press(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.cursor != 0 {
		t.Errorf("pgup at the top should clamp; cursor=%d", m.cursor)
	}

	press(tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.cursor != 1 {
		t.Errorf("ctrl+n should move down one; cursor=%d", m.cursor)
	}
	press(tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.cursor != 0 {
		t.Errorf("ctrl+p should move up one; cursor=%d", m.cursor)
	}
	press(tea.KeyMsg{Type: tea.KeyEnd})
	if m.cursor != len(m.visible)-1 {
		t.Errorf("end should jump to the last row; cursor=%d", m.cursor)
	}
	press(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.cursor != len(m.visible)-1 {
		t.Errorf("pgdn at the bottom should clamp; cursor=%d", m.cursor)
	}
	press(tea.KeyMsg{Type: tea.KeyHome})
	if m.cursor != 0 {
		t.Errorf("home should jump to the first row; cursor=%d", m.cursor)
	}
}

func TestRoborevKeys_LayoutToggle(t *testing.T) {
	// `p` stays as an alias for the pre-roborev binding.
	m, _ := splitFixture(t, 160, 40)
	model, _ := m.Update(runeKey('p'))
	if model.(Model).splitActive() {
		t.Error("p should still hide the pane")
	}

	// From the focused pane: the footer advertises L there, so it has
	// to work there. The issue stays open, full-screen.
	m, _ = splitFixture(t, 160, 40)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.mode != modeDetail {
		t.Fatalf("precondition: enter should focus the pane; mode=%v", m.mode)
	}
	open := m.detailIssue.ID
	model, _ = m.Update(runeKey('L'))
	m = model.(Model)
	if m.splitActive() {
		t.Fatal("L from the focused pane should hide the split")
	}
	if m.mode != modeDetail || m.detailIssue.ID != open {
		t.Errorf("hiding the pane should keep %s open in detail; mode=%v issue=%s", open, m.mode, m.detailIssue.ID)
	}
	if m.detailVP.Width != m.width {
		t.Errorf("detail viewport should be full width (%d) once stacked; got %d", m.width, m.detailVP.Width)
	}
	model, _ = m.Update(runeKey('L'))
	if !model.(Model).splitActive() {
		t.Error("L again should bring the split back")
	}
}

func TestRoborevKeys_FooterAdvertisesSharedKeys(t *testing.T) {
	k := defaultKeyMap()
	for _, c := range []struct{ got, want string }{
		{k.Close.Help().Key, "a"},
		{k.AddNote.Help().Key, "c"},
		{k.Filter.Help().Key, "f"},
		{k.Yank.Help().Key, "y"},
		{k.PaneCopy.Help().Key, "y"},
		{k.Columns.Help().Key, "o"},
		{k.Layout.Help().Key, "L"},
		{k.Help.Help().Key, "?"},
		{k.Quit.Help().Key, "q"},
	} {
		if c.got != c.want {
			t.Errorf("footer key %q, want %q", c.got, c.want)
		}
	}
	var pane []string
	for _, b := range k.paneHelp(true) {
		pane = append(pane, b.Help().Key+" "+b.Help().Desc)
	}
	joined := strings.Join(pane, " | ")
	for _, s := range []string{"a close", "c comment", "y copy instructions", "L show/hide pane"} {
		if !strings.Contains(joined, s) {
			t.Errorf("pane footer missing %q; got %s", s, joined)
		}
	}
}

func TestFilterPrompt_OtherKeysStillWork(t *testing.T) {
	open := func(t *testing.T, w, h int) Model {
		t.Helper()
		m, _ := splitFixture(t, w, h)
		model, _ := m.Update(runeKey('f'))
		m = model.(Model)
		if m.mode != modeFilter {
			t.Fatalf("precondition: f should open the prompt; mode=%v", m.mode)
		}
		return m
	}
	press := func(m Model, k tea.KeyMsg) Model {
		model, _ := m.Update(k)
		return model.(Model)
	}

	// Navigation works with the prompt still open.
	m := open(t, 100, 20)
	page := m.bodyHeight()
	m = press(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.mode != modeFilter || m.cursor != page {
		t.Errorf("pgdn should page with the prompt open; mode=%v cursor=%d want %d", m.mode, m.cursor, page)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m = press(m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.mode != modeFilter || m.cursor != 1 {
		t.Errorf("down then pgup should land on row 1 with the prompt open; mode=%v cursor=%d", m.mode, m.cursor)
	}

	// Letters are text, including ones that are list keys.
	m = open(t, 100, 20)
	for _, r := range "a" {
		m = press(m, runeKey(r))
	}
	if m.mode != modeFilter || m.input.Value() != "a" {
		t.Errorf("a letter should type into the query, not act; mode=%v value=%q", m.mode, m.input.Value())
	}

	// tab cannot coexist with the prompt: it closes it, keeps the query,
	// and moves to the next view.
	m = open(t, 100, 20)
	m = press(m, runeKey('t'))
	before := m.preset
	m = press(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.mode != modeList || m.query != "t" {
		t.Errorf("tab should close the prompt and keep the query; mode=%v query=%q", m.mode, m.query)
	}
	if m.preset == before {
		t.Error("tab should also move to the next view")
	}

	// enter and esc only close it: the selection made while typing is
	// kept, and nothing is opened.
	for _, k := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyEsc}} {
		m = open(t, 100, 20)
		m = press(m, runeKey('t'))
		m = press(m, tea.KeyMsg{Type: tea.KeyDown})
		m = press(m, k)
		if m.mode != modeList || m.query != "t" || m.cursor != 1 {
			t.Errorf("%v should close the prompt, keeping query and selection; mode=%v query=%q cursor=%d", k.Type, m.mode, m.query, m.cursor)
		}
	}
}

func TestFilterPrompt_MouseScrollsAndSelects(t *testing.T) {
	// 100x30 is the stacked layout, 160x40 the split one: the mouse
	// routes differently in each, and both must reach the list.
	for _, size := range [][2]int{{100, 30}, {160, 40}} {
		m, _ := splitFixture(t, size[0], size[1])
		model, _ := m.Update(runeKey('f'))
		m = model.(Model)
		send := func(msg tea.MouseMsg) {
			t.Helper()
			model, _ := m.Update(msg)
			m = model.(Model)
		}

		send(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
		send(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
		if m.mode != modeFilter || m.cursor != 2 {
			t.Fatalf("%v: the wheel should move the selection with the prompt open; mode=%v cursor=%d", size, m.mode, m.cursor)
		}

		// A click selects a row and leaves the prompt up.
		click := tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 4, Y: m.rowsStartY() + 5}
		send(click)
		if m.mode != modeFilter || m.cursor != 5 {
			t.Fatalf("%v: a click should select row 5 with the prompt open; mode=%v cursor=%d", size, m.mode, m.cursor)
		}
		send(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 4, Y: click.Y})

		// Typing still goes to the query afterwards.
		model, _ = m.Update(runeKey('i'))
		m = model.(Model)
		if m.mode != modeFilter || m.input.Value() != "i" {
			t.Fatalf("%v: typing after a click should still edit the query; mode=%v value=%q", size, m.mode, m.input.Value())
		}

		// A second click on the selected row opens it: the prompt
		// closes, the query stays applied.
		send(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 4, Y: m.rowsStartY() + 1})
		target := m.visible[m.cursor].ID
		send(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 4, Y: m.rowsStartY() + 1})
		send(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 4, Y: m.rowsStartY() + 1})
		if m.mode != modeDetail || m.detailIssue.ID != target || m.query != "i" {
			t.Errorf("%v: a second click should open %s and keep the query; mode=%v issue=%s query=%q", size, target, m.mode, m.detailIssue.ID, m.query)
		}
	}
}

func TestFilterPrompt_FooterShowsWhatWorks(t *testing.T) {
	m, _ := splitFixture(t, 160, 40)
	model, _ := m.Update(runeKey('f'))
	m = model.(Model)
	var got []string
	for _, b := range m.footerBindings() {
		got = append(got, b.Help().Key)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"↑/↓", "pgup/pgdn", "tab", "⏎", "esc"} {
		if !strings.Contains(joined, want) {
			t.Errorf("prompt footer missing %q; got %s", want, joined)
		}
	}
	if strings.Contains(joined, "j/k") {
		t.Errorf("prompt footer must not advertise j/k, which type here; got %s", joined)
	}
}
